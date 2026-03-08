package wallet

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/chaincfg"
)

const (
	// CompatibilityExpiryWindowBlocks is the conservative fallback used when
	// no OBTC-aware source is available.
	CompatibilityExpiryWindowBlocks uint64 = 3679200

	// CompatibilityDustThresholdSat is the fallback dust threshold used when
	// no OBTC-aware source is available.
	CompatibilityDustThresholdSat int64 = 546

	// DefaultProjectedReclaimRatioBps is the default reclaim ratio used by
	// wallet-side expiry UX and planning layers.
	DefaultProjectedReclaimRatioBps uint32 = 7000

	// OBTCChaincfgPathEnv points to either `params_obtc.go`, the `chaincfg`
	// directory that contains it, or the `obtcd` repo root.
	OBTCChaincfgPathEnv = "OBTC_CHAINCFG_PATH"

	// OBTCExpiryNetworkEnv optionally forces the logical OBTC network name
	// used for expiry parameter resolution.
	OBTCExpiryNetworkEnv = "OBTC_EXPIRY_NETWORK"

	maxDefaultExpiringThresholdBlocks int32 = 144 * 180
)

type ResolvedExpiryPolicy struct {
	WindowBlocks             uint64
	ExpiringThresholdBlocks  int32
	DustThresholdSat         int64
	ProjectedReclaimRatioBps uint32
	Source                   string
}

type obtcExpiryProfile struct {
	NetworkName      string
	WindowBlocks     uint64
	DustThresholdSat int64
}

var builtInOBTCExpiryProfiles = map[string]obtcExpiryProfile{
	"obtcmainnet": {
		NetworkName:      "obtcmainnet",
		WindowBlocks:     362880,
		DustThresholdSat: 720,
	},
	"obtctestnet": {
		NetworkName:      "obtctestnet",
		WindowBlocks:     1008,
		DustThresholdSat: 720,
	},
	"obtcregtest": {
		NetworkName:      "obtcregtest",
		WindowBlocks:     144,
		DustThresholdSat: 720,
	},
}

var obtcExpiryCaseToVarName = map[string]string{
	"ObtcMainNet": "ObtcMainNetParams",
	"ObtcTestNet": "ObtcTestNetParams",
	"ObtcRegNet":  "ObtcRegTestParams",
}

// ResolveExpiryPolicy resolves wallet-side expiry parameters from the current
// chain parameters. It prefers a real OBTC source file when available, then
// built-in OBTC defaults, and finally compatibility defaults.
func ResolveExpiryPolicy(params *chaincfg.Params) (ResolvedExpiryPolicy, []string) {
	networkOverride := strings.TrimSpace(os.Getenv(OBTCExpiryNetworkEnv))
	return resolveExpiryPolicyFromSources(
		params, candidateOBTCExpirySourcePaths(), networkOverride,
	)
}

func resolveExpiryPolicyFromSources(params *chaincfg.Params, sourcePaths []string,
	networkOverride string) (ResolvedExpiryPolicy, []string) {

	policy := ResolvedExpiryPolicy{
		WindowBlocks:             CompatibilityExpiryWindowBlocks,
		DustThresholdSat:         CompatibilityDustThresholdSat,
		ProjectedReclaimRatioBps: DefaultProjectedReclaimRatioBps,
		Source:                   "compatibility_default",
	}
	warnings := make([]string, 0, 4)

	candidates := expiryNetworkCandidates(params, networkOverride)
	if len(candidates) == 0 {
		policy.ExpiringThresholdBlocks = defaultExpiringThresholdBlocks(
			policy.WindowBlocks,
		)
		warnings = append(warnings,
			"wallet chain parameters do not identify an OBTC network; using compatibility expiry defaults")
		return policy, warnings
	}

	profiles, usedPath, err := loadOBTCExpiryProfilesFromPaths(sourcePaths)
	if err == nil {
		for _, candidate := range candidates {
			profile, ok := profiles[candidate]
			if !ok {
				continue
			}

			policy.WindowBlocks = profile.WindowBlocks
			policy.DustThresholdSat = profile.DustThresholdSat
			policy.Source = fmt.Sprintf("obtcd_chaincfg:%s", usedPath)
			policy.ExpiringThresholdBlocks = defaultExpiringThresholdBlocks(
				policy.WindowBlocks,
			)
			if networkOverride != "" {
				warnings = append(warnings,
					fmt.Sprintf("using %s=%s to select OBTC expiry parameters",
						OBTCExpiryNetworkEnv, candidate))
			}
			return policy, warnings
		}
	}

	for _, candidate := range candidates {
		profile, ok := builtInOBTCExpiryProfiles[candidate]
		if !ok {
			continue
		}

		policy.WindowBlocks = profile.WindowBlocks
		policy.DustThresholdSat = profile.DustThresholdSat
		policy.Source = "obtc_builtin_fallback"
		policy.ExpiringThresholdBlocks = defaultExpiringThresholdBlocks(
			policy.WindowBlocks,
		)
		if err != nil {
			warnings = append(warnings,
				fmt.Sprintf("unable to load OBTC expiry params from source file: %v", err))
		}
		warnings = append(warnings,
			"using built-in OBTC expiry defaults; set OBTC_CHAINCFG_PATH to point at obtcd/chaincfg/params_obtc.go for source-of-truth resolution")
		if networkOverride != "" {
			warnings = append(warnings,
				fmt.Sprintf("using %s=%s to select OBTC expiry parameters",
					OBTCExpiryNetworkEnv, candidate))
		}
		return policy, warnings
	}

	policy.ExpiringThresholdBlocks = defaultExpiringThresholdBlocks(
		policy.WindowBlocks,
	)
	if err != nil {
		warnings = append(warnings,
			fmt.Sprintf("unable to load OBTC expiry params from source file: %v", err))
	}
	warnings = append(warnings,
		"OBTC expiry parameters were not found for this network; using compatibility expiry defaults")

	return policy, warnings
}

func defaultExpiringThresholdBlocks(windowBlocks uint64) int32 {
	if windowBlocks == 0 {
		return maxDefaultExpiringThresholdBlocks
	}

	threshold := windowBlocks / 4
	if threshold == 0 {
		threshold = 1
	}
	if threshold > uint64(maxDefaultExpiringThresholdBlocks) {
		threshold = uint64(maxDefaultExpiringThresholdBlocks)
	}

	return int32(threshold)
}

func expiryNetworkCandidates(params *chaincfg.Params,
	networkOverride string) []string {

	candidates := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	add := func(name string) {
		name = normalizeExpiryNetworkName(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		candidates = append(candidates, name)
	}

	add(networkOverride)
	if params != nil {
		add(params.Name)
	}

	return candidates
}

func normalizeExpiryNetworkName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func candidateOBTCExpirySourcePaths() []string {
	candidates := make([]string, 0, 8)
	add := func(path string) {
		if path == "" {
			return
		}
		candidates = append(candidates, path)
	}
	addExpanded := func(path string) {
		if path == "" {
			return
		}
		if strings.HasSuffix(path, ".go") {
			add(path)
			return
		}

		add(filepath.Join(path, "params_obtc.go"))
		add(filepath.Join(path, "chaincfg", "params_obtc.go"))
	}

	addExpanded(strings.TrimSpace(os.Getenv(OBTCChaincfgPathEnv)))

	if _, file, _, ok := runtime.Caller(0); ok {
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
		add(filepath.Join(repoRoot, "..", "obtcd", "chaincfg", "params_obtc.go"))
	}

	if wd, err := os.Getwd(); err == nil {
		add(filepath.Join(wd, "chaincfg", "params_obtc.go"))
		add(filepath.Join(wd, "obtcd", "chaincfg", "params_obtc.go"))
		add(filepath.Join(wd, "..", "obtcd", "chaincfg", "params_obtc.go"))
	}

	return dedupePaths(candidates)
}

func dedupePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		cleaned := filepath.Clean(path)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		unique = append(unique, cleaned)
	}
	return unique
}

func loadOBTCExpiryProfilesFromPaths(paths []string) (
	map[string]obtcExpiryProfile, string, error) {

	var firstErr error
	for _, path := range dedupePaths(paths) {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}

		profiles, err := parseOBTCExpiryProfilesFile(path)
		if err == nil {
			return profiles, path, nil
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", path, err)
		}
	}

	if firstErr != nil {
		return nil, "", firstErr
	}

	return nil, "", fmt.Errorf("no params_obtc.go source found")
}

func parseOBTCExpiryProfilesFile(path string) (map[string]obtcExpiryProfile, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}

	constValues := make(map[string]int64)
	paramNames := make(map[string]string)
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}

		switch genDecl.Tok {
		case token.CONST:
			collectConstValues(genDecl, constValues)

		case token.VAR:
			collectOBTCParamNames(genDecl, paramNames)
		}
	}

	expiryProfilesByCase := make(map[string]obtcExpiryProfile)
	for _, decl := range file.Decls {
		funcDecl, ok := decl.(*ast.FuncDecl)
		if !ok || funcDecl.Name == nil || funcDecl.Name.Name != "GetExpiryParams" {
			continue
		}

		parsedProfiles, err := parseGetExpiryParamsFunc(funcDecl, constValues)
		if err != nil {
			return nil, err
		}
		for key, profile := range parsedProfiles {
			expiryProfilesByCase[key] = profile
		}
	}

	profiles := make(map[string]obtcExpiryProfile, len(expiryProfilesByCase))
	for caseName, profile := range expiryProfilesByCase {
		varName, ok := obtcExpiryCaseToVarName[caseName]
		if !ok {
			continue
		}
		networkName, ok := paramNames[varName]
		if !ok {
			continue
		}
		profile.NetworkName = networkName
		profiles[normalizeExpiryNetworkName(networkName)] = profile
	}

	if len(profiles) == 0 {
		return nil, fmt.Errorf("no OBTC expiry profiles parsed from source")
	}

	return profiles, nil
}

func collectConstValues(genDecl *ast.GenDecl, constValues map[string]int64) {
	for _, spec := range genDecl.Specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for idx, name := range valueSpec.Names {
			if idx >= len(valueSpec.Values) {
				continue
			}
			value, err := evalIntExpr(valueSpec.Values[idx], constValues)
			if err != nil {
				continue
			}
			constValues[name.Name] = value
		}
	}
}

func collectOBTCParamNames(genDecl *ast.GenDecl, paramNames map[string]string) {
	for _, spec := range genDecl.Specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if !ok || len(valueSpec.Names) == 0 || len(valueSpec.Values) == 0 {
			continue
		}

		name := valueSpec.Names[0].Name
		if _, ok := obtcExpiryCaseToVarNameReverse(name); !ok {
			continue
		}

		composite, ok := valueSpec.Values[0].(*ast.CompositeLit)
		if !ok {
			continue
		}

		for _, elt := range composite.Elts {
			keyValue, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := keyValue.Key.(*ast.Ident)
			if !ok || key.Name != "Name" {
				continue
			}
			lit, ok := keyValue.Value.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			networkName, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			paramNames[name] = networkName
		}
	}
}

func obtcExpiryCaseToVarNameReverse(varName string) (string, bool) {
	for caseName, candidate := range obtcExpiryCaseToVarName {
		if candidate == varName {
			return caseName, true
		}
	}
	return "", false
}

func parseGetExpiryParamsFunc(funcDecl *ast.FuncDecl,
	constValues map[string]int64) (map[string]obtcExpiryProfile, error) {

	profiles := make(map[string]obtcExpiryProfile)
	ast.Inspect(funcDecl.Body, func(node ast.Node) bool {
		switchStmt, ok := node.(*ast.SwitchStmt)
		if !ok {
			return true
		}

		for _, stmt := range switchStmt.Body.List {
			caseClause, ok := stmt.(*ast.CaseClause)
			if !ok || len(caseClause.List) == 0 {
				continue
			}

			caseName := selectorName(caseClause.List[0])
			if caseName == "" {
				continue
			}

			profile, ok := extractExpiryProfileFromCase(caseClause, constValues)
			if !ok {
				continue
			}
			profiles[caseName] = profile
		}

		return false
	})

	if len(profiles) == 0 {
		return nil, fmt.Errorf("GetExpiryParams did not contain parseable OBTC cases")
	}

	return profiles, nil
}

func selectorName(expr ast.Expr) string {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	return selector.Sel.Name
}

func extractExpiryProfileFromCase(caseClause *ast.CaseClause,
	constValues map[string]int64) (obtcExpiryProfile, bool) {

	for _, stmt := range caseClause.Body {
		returnStmt, ok := stmt.(*ast.ReturnStmt)
		if !ok || len(returnStmt.Results) == 0 {
			continue
		}

		composite := unwrapCompositeLit(returnStmt.Results[0])
		if composite == nil {
			continue
		}

		profile, err := extractExpiryProfileFromCompositeLit(
			composite, constValues,
		)
		if err != nil {
			continue
		}
		return profile, true
	}

	return obtcExpiryProfile{}, false
}

func unwrapCompositeLit(expr ast.Expr) *ast.CompositeLit {
	switch e := expr.(type) {
	case *ast.UnaryExpr:
		return unwrapCompositeLit(e.X)
	case *ast.CompositeLit:
		return e
	default:
		return nil
	}
}

func extractExpiryProfileFromCompositeLit(composite *ast.CompositeLit,
	constValues map[string]int64) (obtcExpiryProfile, error) {

	var (
		windowBlocks     uint64
		dustThresholdSat int64
		haveWindow       bool
		haveDust         bool
	)

	for _, elt := range composite.Elts {
		keyValue, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := keyValue.Key.(*ast.Ident)
		if !ok {
			continue
		}

		value, err := evalIntExpr(keyValue.Value, constValues)
		if err != nil {
			continue
		}

		switch key.Name {
		case "WindowBlocks":
			windowBlocks = uint64(value)
			haveWindow = true

		case "ReapDustThresholdSat":
			dustThresholdSat = value
			haveDust = true
		}
	}

	if !haveWindow || !haveDust {
		return obtcExpiryProfile{}, fmt.Errorf("missing window or dust fields")
	}

	return obtcExpiryProfile{
		WindowBlocks:     windowBlocks,
		DustThresholdSat: dustThresholdSat,
	}, nil
}

func evalIntExpr(expr ast.Expr, constValues map[string]int64) (int64, error) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.INT {
			return 0, fmt.Errorf("unsupported literal kind %s", e.Kind)
		}
		return strconv.ParseInt(e.Value, 0, 64)

	case *ast.Ident:
		value, ok := constValues[e.Name]
		if !ok {
			return 0, fmt.Errorf("unknown identifier %s", e.Name)
		}
		return value, nil

	case *ast.BinaryExpr:
		left, err := evalIntExpr(e.X, constValues)
		if err != nil {
			return 0, err
		}
		right, err := evalIntExpr(e.Y, constValues)
		if err != nil {
			return 0, err
		}

		switch e.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return left / right, nil
		case token.REM:
			if right == 0 {
				return 0, fmt.Errorf("modulo by zero")
			}
			return left % right, nil
		case token.SHL:
			return left << uint(right), nil
		case token.SHR:
			return left >> uint(right), nil
		default:
			return 0, fmt.Errorf("unsupported binary op %s", e.Op)
		}

	case *ast.UnaryExpr:
		value, err := evalIntExpr(e.X, constValues)
		if err != nil {
			return 0, err
		}
		switch e.Op {
		case token.ADD:
			return value, nil
		case token.SUB:
			return -value, nil
		default:
			return 0, fmt.Errorf("unsupported unary op %s", e.Op)
		}

	case *ast.ParenExpr:
		return evalIntExpr(e.X, constValues)

	default:
		return 0, fmt.Errorf("unsupported expression type %T", expr)
	}
}
