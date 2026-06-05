(function () {
  "use strict";

  const token = document.querySelector('meta[name="wallet-app-token"]').content;
  const rpcEndpoint = document.querySelector('meta[name="wallet-rpc-endpoint"]').content;

  const els = {
    rpcEndpoint: document.getElementById("rpcEndpoint"),
    connectionState: document.getElementById("connectionState"),
    refreshButton: document.getElementById("refreshButton"),
    spendableBalance: document.getElementById("spendableBalance"),
    totalBalance: document.getElementById("totalBalance"),
    unconfirmedBalance: document.getElementById("unconfirmedBalance"),
    blockHeight: document.getElementById("blockHeight"),
    lockState: document.getElementById("lockState"),
    receiveAddress: document.getElementById("receiveAddress"),
    newAddressButton: document.getElementById("newAddressButton"),
    copyAddressButton: document.getElementById("copyAddressButton"),
    sendForm: document.getElementById("sendForm"),
    sendAddress: document.getElementById("sendAddress"),
    sendAmount: document.getElementById("sendAmount"),
    expiryLimit: document.getElementById("expiryLimit"),
    loadExpiryButton: document.getElementById("loadExpiryButton"),
    expiryTableBody: document.getElementById("expiryTableBody"),
    renewForm: document.getElementById("renewForm"),
    renewSelectionSummary: document.getElementById("renewSelectionSummary"),
    renewFeeRate: document.getElementById("renewFeeRate"),
    renewMinConf: document.getElementById("renewMinConf"),
    renewSubmitButton: document.getElementById("renewSubmitButton"),
    renewStatus: document.getElementById("renewStatus"),
    recentRenew: document.getElementById("recentRenew"),
    unlockForm: document.getElementById("unlockForm"),
    walletPassphrase: document.getElementById("walletPassphrase"),
    unlockSeconds: document.getElementById("unlockSeconds"),
    lockButton: document.getElementById("lockButton"),
    loadTransactionsButton: document.getElementById("loadTransactionsButton"),
    activityList: document.getElementById("activityList"),
    resultBox: document.getElementById("resultBox"),
    clearResultButton: document.getElementById("clearResultButton"),
  };

  const state = {
    expiryItems: [],
    selectedOutpoints: new Set(),
    renewMarks: new Map(),
    lastRenew: null,
  };

  function init() {
    els.rpcEndpoint.textContent = rpcEndpoint;
    els.refreshButton.addEventListener("click", refreshAll);
    els.newAddressButton.addEventListener("click", createAddress);
    els.copyAddressButton.addEventListener("click", copyAddress);
    els.loadExpiryButton.addEventListener("click", loadExpiry);
    els.loadTransactionsButton.addEventListener("click", loadTransactions);
    els.clearResultButton.addEventListener("click", () => setResult("Ready"));
    els.unlockForm.addEventListener("submit", unlockWallet);
    els.lockButton.addEventListener("click", lockWallet);
    els.sendForm.addEventListener("submit", sendFunds);
    els.renewForm.addEventListener("submit", renewSelected);
    refreshAll();
  }

  async function api(path, options = {}) {
    const headers = new Headers(options.headers || {});
    headers.set("X-Wallet-App-Token", token);
    if (options.body !== undefined) {
      headers.set("Content-Type", "application/json");
    }

    const response = await fetch(path, {
      cache: "no-store",
      ...options,
      headers,
    });
    const text = await response.text();
    let data = null;
    if (text) {
      try {
        data = JSON.parse(text);
      } catch (err) {
        throw new Error(text);
      }
    }
    if (!response.ok) {
      const message = data && data.error && data.error.message
        ? data.error.message
        : response.statusText;
      throw new Error(message);
    }
    return data;
  }

  async function refreshAll() {
    await loadState();
    await loadExpiry();
  }

  async function loadState() {
    try {
      const data = await api("/api/state");
      setConnection(!data.partial_errors);
      renderState(data);
      renderTransactions(data.recent_transactions || []);
      if (data.partial_errors) {
        setResult(data);
      }
    } catch (err) {
      setConnection(false);
      setResult(err.message);
    }
  }

  function renderState(data) {
    els.spendableBalance.textContent = formatAmount(data.spendable_balance);
    els.totalBalance.textContent = formatAmount(data.balance);
    els.unconfirmedBalance.textContent = formatAmount(data.unconfirmed_balance);
    els.blockHeight.textContent = data.block_count == null ? "--" : String(data.block_count);
    els.renewFeeRate.textContent = formatFeeRate(data.renew_fee_rate_sat_per_kb);

    els.lockState.classList.remove("locked", "unlocked");
    if (data.locked === true) {
      els.lockState.textContent = "Locked";
      els.lockState.classList.add("locked");
    } else if (data.locked === false) {
      els.lockState.textContent = "Unlocked";
      els.lockState.classList.add("unlocked");
    } else {
      els.lockState.textContent = "--";
    }
  }

  async function createAddress() {
    const addressType = document.querySelector('input[name="addressType"]:checked').value;
    try {
      const data = await api("/api/address/new", {
        method: "POST",
        body: JSON.stringify({ address_type: addressType }),
      });
      els.receiveAddress.value = data.address || "";
      setResult(data);
    } catch (err) {
      setResult(err.message);
    }
  }

  async function copyAddress() {
    if (!els.receiveAddress.value) {
      return;
    }
    try {
      await navigator.clipboard.writeText(els.receiveAddress.value);
      setResult("Address copied");
    } catch (err) {
      els.receiveAddress.select();
      document.execCommand("copy");
      setResult("Address copied");
    }
  }

  async function unlockWallet(event) {
    event.preventDefault();
    const passphrase = els.walletPassphrase.value;
    const timeoutSeconds = parseInteger(els.unlockSeconds.value, 300);
    try {
      const data = await api("/api/unlock", {
        method: "POST",
        body: JSON.stringify({
          passphrase,
          timeout_seconds: timeoutSeconds,
        }),
      });
      els.walletPassphrase.value = "";
      setResult(data);
      await loadState();
    } catch (err) {
      setResult(err.message);
    }
  }

  async function lockWallet() {
    try {
      const data = await api("/api/lock", { method: "POST" });
      setResult(data);
      await loadState();
    } catch (err) {
      setResult(err.message);
    }
  }

  async function sendFunds(event) {
    event.preventDefault();
    const address = els.sendAddress.value.trim();
    const amount = parseAmount(els.sendAmount.value);
    if (!address || amount <= 0) {
      setResult("Address and amount are required");
      return;
    }
    if (!window.confirm(`Send ${amount} OBTC to ${address}?`)) {
      return;
    }

    try {
      const data = await api("/api/send", {
        method: "POST",
        body: JSON.stringify({ address, amount }),
      });
      setResult(data);
      await refreshAll();
    } catch (err) {
      setResult(err.message);
    }
  }

  async function loadExpiry() {
    const limit = Math.max(0, parseInteger(els.expiryLimit.value, 100));
    try {
      const data = await api(`/api/expiry?limit=${encodeURIComponent(limit)}`);
      state.expiryItems = data.items || [];
      state.selectedOutpoints.clear();
      renderExpiry(data);
      updateRenewSelectionSummary();
    } catch (err) {
      renderExpiry({ items: [] });
      updateRenewSelectionSummary();
      setResult(err.message);
    }
  }

  function renderExpiry(data) {
    const items = data.items || [];
    els.expiryTableBody.innerHTML = "";
    if (items.length === 0) {
      const row = document.createElement("tr");
      row.innerHTML = '<td colspan="5" class="empty">No expiry data</td>';
      els.expiryTableBody.appendChild(row);
      return;
    }

    for (const item of items) {
      const row = document.createElement("tr");
      const status = String(item.status || "unknown");
      const renewable = isRenewableItem(item);
      const renewMark = state.renewMarks.get(item.outpoint);
      if (renewMark) {
        row.classList.add("renew-row", escapeAttribute(renewMark.status));
      }
      if (!renewable) {
        row.classList.add("not-renewable");
        state.selectedOutpoints.delete(item.outpoint);
      }
      row.innerHTML = `
        <td><input type="checkbox" aria-label="Select outpoint" /></td>
        <td>
          <span class="row-status ${escapeAttribute(status)}">${escapeHTML(status)}</span>
          ${renewable ? "" : '<span class="renew-marker too-late">Too late</span>'}
          ${renderRenewMarker(item.outpoint)}
        </td>
        <td>${formatSat(item.amount_sat)}</td>
        <td>${escapeHTML(String(item.expiry_height ?? "--"))}</td>
        <td><code>${escapeHTML(item.outpoint || "")}</code></td>
      `;
      const checkbox = row.querySelector("input");
      checkbox.disabled = !renewable;
      checkbox.title = renewable ? "" : "This outpoint cannot be renewed because it expires at the next spend height.";
      checkbox.checked = renewable && state.selectedOutpoints.has(item.outpoint);
      checkbox.addEventListener("change", () => {
        if (checkbox.checked) {
          state.selectedOutpoints.add(item.outpoint);
        } else {
          state.selectedOutpoints.delete(item.outpoint);
        }
        updateRenewSelectionSummary();
      });
      els.expiryTableBody.appendChild(row);
    }
  }

  async function renewSelected(event) {
    event.preventDefault();
    const outpoints = Array.from(state.selectedOutpoints);
    if (outpoints.length === 0) {
      setResult("Select at least one outpoint");
      return;
    }
    const notRenewable = outpoints.filter((outpoint) => {
      const item = state.expiryItems.find((candidate) => candidate.outpoint === outpoint);
      return !item || !isRenewableItem(item);
    });
    if (notRenewable.length > 0) {
      for (const outpoint of notRenewable) {
        state.selectedOutpoints.delete(outpoint);
      }
      renderExpiry({ items: state.expiryItems });
      updateRenewSelectionSummary();
      setRenewStatus("error", "Selected outpoint is too close to expiry; reload and choose an outpoint with at least 2 blocks remaining.");
      return;
    }
    const totalSat = selectedRenewTotalSat();
    if (!window.confirm(`Renew ${outpoints.length} outpoint(s), total ${formatSat(totalSat)}?`)) {
      return;
    }

    const submittedAt = new Date();
    markRenewOutpoints(outpoints, "pending");
    state.lastRenew = {
      outpoints,
      totalSat,
      status: "pending",
      txid: "",
      message: "Submitting renew transaction",
      submittedAt,
    };
    renderLastRenew();
    setRenewStatus("pending", `Submitting renew for ${outpoints.length} outpoint(s)`);
    setRenewBusy(true);
    renderExpiry({ items: state.expiryItems });

    const body = { outpoints };
    const minConf = parseOptionalInteger(els.renewMinConf.value);
    if (minConf !== null) {
      body.min_conf = minConf;
    }

    try {
      const data = await api("/api/renew", {
        method: "POST",
        body: JSON.stringify(body),
      });
      markRenewOutpoints(outpoints, "renewed", data.txid || "");
      state.lastRenew = {
        outpoints,
        totalSat,
        status: "renewed",
        txid: data.txid || "",
        message: `Renew confirmed${data.txid ? `: ${data.txid}` : ""}`,
        submittedAt,
        completedAt: new Date(),
      };
      renderLastRenew();
      setRenewStatus("success", state.lastRenew.message);
      setResult(data);
      await refreshAll();
    } catch (err) {
      markRenewOutpoints(outpoints, "failed", "", err.message);
      state.lastRenew = {
        outpoints,
        totalSat,
        status: "failed",
        txid: "",
        message: err.message,
        submittedAt,
        completedAt: new Date(),
      };
      renderLastRenew();
      setRenewStatus("error", `Renew failed: ${err.message}`);
      renderExpiry({ items: state.expiryItems });
      setResult(err.message);
    } finally {
      setRenewBusy(false);
    }
  }

  function selectedRenewTotalSat() {
    const selected = state.selectedOutpoints;
    return state.expiryItems.reduce((sum, item) => {
      if (!selected.has(item.outpoint)) {
        return sum;
      }
      return sum + Number(item.amount_sat || 0);
    }, 0);
  }

  function isRenewableItem(item) {
    const blocksToExpiry = Number(item.blocks_to_expiry);
    if (!Number.isFinite(blocksToExpiry)) {
      return false;
    }
    return String(item.status || "") !== "expired" && blocksToExpiry > 1;
  }

  function updateRenewSelectionSummary() {
    const count = state.selectedOutpoints.size;
    if (count === 0) {
      els.renewSelectionSummary.textContent = "0 selected";
      return;
    }
    els.renewSelectionSummary.textContent = `${count} selected / ${formatSat(selectedRenewTotalSat())}`;
  }

  function setRenewBusy(isBusy) {
    els.renewSubmitButton.disabled = isBusy;
    els.renewSubmitButton.textContent = isBusy ? "Renewing" : "Renew selected";
    els.loadExpiryButton.disabled = isBusy;
  }

  function setRenewStatus(kind, message) {
    els.renewStatus.classList.remove("idle", "pending", "success", "error");
    els.renewStatus.classList.add(kind);
    els.renewStatus.textContent = message;
  }

  function markRenewOutpoints(outpoints, status, txid = "", message = "") {
    for (const outpoint of outpoints) {
      state.renewMarks.set(outpoint, { status, txid, message });
    }
  }

  function renderRenewMarker(outpoint) {
    const mark = state.renewMarks.get(outpoint);
    if (!mark) {
      return "";
    }
    const label = mark.status === "renewed"
      ? "Renewed"
      : mark.status === "failed"
        ? "Failed"
        : "Pending";
    return `<span class="renew-marker ${escapeAttribute(mark.status)}">${label}</span>`;
  }

  function renderLastRenew() {
    const last = state.lastRenew;
    if (!last) {
      els.recentRenew.hidden = true;
      els.recentRenew.innerHTML = "";
      return;
    }

    els.recentRenew.hidden = false;
    const txid = last.txid
      ? `<code>${escapeHTML(last.txid)}</code>`
      : "<span>--</span>";
    const outpoints = last.outpoints
      .map((outpoint) => `<li><code>${escapeHTML(outpoint)}</code></li>`)
      .join("");

    els.recentRenew.innerHTML = `
      <div class="recent-renew-head">
        <strong>${escapeHTML(statusTitle(last.status))}</strong>
        <span>${escapeHTML(formatSat(last.totalSat))}</span>
      </div>
      <p>${escapeHTML(last.message)}</p>
      <div class="recent-renew-meta">
        <span>${escapeHTML(formatTime(last.completedAt || last.submittedAt))}</span>
        ${txid}
      </div>
      <ul>${outpoints}</ul>
    `;
  }

  function statusTitle(status) {
    switch (status) {
    case "renewed":
      return "Last renewed";
    case "failed":
      return "Renew failed";
    default:
      return "Renew pending";
    }
  }

  async function loadTransactions() {
    try {
      const data = await api("/api/transactions?count=50");
      renderTransactions(data.transactions || []);
      setResult(data);
    } catch (err) {
      setResult(err.message);
    }
  }

  function renderTransactions(transactions) {
    els.activityList.innerHTML = "";
    if (transactions.length === 0) {
      const empty = document.createElement("div");
      empty.className = "activity-item";
      empty.innerHTML = "<span>No transactions</span>";
      els.activityList.appendChild(empty);
      return;
    }

    for (const tx of transactions.slice(0, 20)) {
      const item = document.createElement("div");
      item.className = "activity-item";
      item.innerHTML = `
        <strong>${escapeHTML(tx.category || "transaction")} ${formatAmount(tx.amount)}</strong>
        <code>${escapeHTML(tx.txid || "")}</code>
        <span>${escapeHTML(String(tx.confirmations ?? 0))} confirmation(s)</span>
      `;
      els.activityList.appendChild(item);
    }
  }

  function setConnection(ok) {
    els.connectionState.textContent = ok ? "Connected" : "Offline";
    els.connectionState.classList.toggle("bad", !ok);
  }

  function setResult(value) {
    if (typeof value === "string") {
      els.resultBox.textContent = value;
      return;
    }
    els.resultBox.textContent = JSON.stringify(value, null, 2);
  }

  function formatAmount(value) {
    if (value == null || Number.isNaN(Number(value))) {
      return "--";
    }
    return `${Number(value).toFixed(8)} OBTC`;
  }

  function formatFeeRate(value) {
    const num = Number(value);
    if (!Number.isFinite(num) || num <= 0) {
      return "--";
    }
    return `${num.toLocaleString()} sat/KB`;
  }

  function formatSat(value) {
    if (value == null || Number.isNaN(Number(value))) {
      return "--";
    }
    return `${Number(value).toLocaleString()} sat`;
  }

  function formatTime(value) {
    if (!(value instanceof Date)) {
      return "--";
    }
    return value.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  }

  function parseAmount(value) {
    const n = Number(String(value).trim());
    return Number.isFinite(n) ? n : 0;
  }

  function parseInteger(value, fallback) {
    const n = Number.parseInt(String(value).trim(), 10);
    return Number.isFinite(n) ? n : fallback;
  }

  function parseOptionalInteger(value) {
    if (String(value).trim() === "") {
      return null;
    }
    const n = parseInteger(value, -1);
    return n >= 0 ? n : null;
  }

  function escapeHTML(value) {
    return String(value)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#039;");
  }

  function escapeAttribute(value) {
    return String(value).replace(/[^a-z0-9_-]/gi, "").toLowerCase();
  }

  init();
})();
