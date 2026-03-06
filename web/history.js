(() => {
  const App = window.DiskTreemapApp;

  const els = {
    rootPath: document.getElementById("rootPath"),
    scanState: document.getElementById("scanState"),
    scanBanner: document.getElementById("scanBanner"),
    scanBannerSummary: document.getElementById("scanBannerSummary"),
    scanBannerDetails: document.getElementById("scanBannerDetails"),
    appAlert: document.getElementById("appAlert"),
    appAlertText: document.getElementById("appAlertText"),
    alertRetryButton: document.getElementById("alertRetryButton"),
    summaryLatest: document.getElementById("summaryLatest"),
    summarySaved: document.getElementById("summarySaved"),
    summaryActive: document.getElementById("summaryActive"),
    summaryCompare: document.getElementById("summaryCompare"),
    openLatestButton: document.getElementById("openLatestButton"),
    runScanButton: document.getElementById("runScanButton"),
    toggleCompareButton: document.getElementById("toggleCompareButton"),
    compareSection: document.getElementById("compareSection"),
    compareIntro: document.getElementById("compareIntro"),
    baseScanSelect: document.getElementById("baseScanSelect"),
    targetScanSelect: document.getElementById("targetScanSelect"),
    compareBreadcrumb: document.getElementById("compareBreadcrumb"),
    compareSummaryStrip: document.getElementById("compareSummaryStrip"),
    compareBefore: document.getElementById("compareBefore"),
    compareAfter: document.getElementById("compareAfter"),
    compareDelta: document.getElementById("compareDelta"),
    compareChanged: document.getElementById("compareChanged"),
    compareEmpty: document.getElementById("compareEmpty"),
    compareList: document.getElementById("compareList"),
    historySummary: document.getElementById("historySummary"),
    refreshHistoryButton: document.getElementById("refreshHistoryButton"),
    historyList: document.getElementById("historyList"),
    historyEmpty: document.getElementById("historyEmpty"),
  };

  const state = {
    config: null,
    historyItems: [],
    alert: null,
    pollingHandle: null,
    pollingScanId: null,
    compareOpen: false,
    compareLoading: false,
    compareBaseId: null,
    compareTargetId: null,
    comparePath: null,
    compareResult: null,
    urlState: App.readHistoryUrlState(),
  };

  init().catch((err) => {
    state.alert = {
      message: `Unable to load scan history: ${err.message}`,
      retry: false,
    };
    App.renderStatusChip(els.scanState, { status: "failed" });
    renderAll();
  });

  async function init() {
    bindEvents();
    renderAll();

    const cfg = await App.apiGet("/api/v1/config");
    state.config = cfg;
    state.compareBaseId = state.urlState.baseScanId;
    state.compareTargetId = state.urlState.targetScanId;
    state.comparePath = state.urlState.path || cfg.analyze_root;
    state.compareOpen = Boolean(state.compareBaseId && state.compareTargetId);

    renderRootPath();
    await loadHistory();

    if (canCompare()) {
      await loadCompare(state.comparePath);
    }
  }

  function bindEvents() {
    els.alertRetryButton.addEventListener("click", runScan);
    els.openLatestButton.addEventListener("click", openLatestScan);
    els.runScanButton.addEventListener("click", runScan);
    els.toggleCompareButton.addEventListener("click", () => {
      state.compareOpen = !state.compareOpen;
      if (!state.compareOpen) {
        state.compareResult = null;
      }
      renderCompare();
      syncUrlState();
    });
    els.baseScanSelect.addEventListener("change", handleCompareSelectionChange);
    els.targetScanSelect.addEventListener("change", handleCompareSelectionChange);
    els.refreshHistoryButton.addEventListener("click", () => {
      loadHistory().catch((err) => showAlert(`Could not refresh scan history: ${err.message}`));
    });
  }

  function renderAll() {
    renderRootPath();
    renderBanner();
    renderAlert();
    renderSummary();
    renderHeroActions();
    renderHistory();
    renderCompare();
  }

  function renderRootPath() {
    if (!state.config?.analyze_root) {
      els.rootPath.textContent = "Loading root path...";
      return;
    }

    els.rootPath.textContent = `Root: ${state.config.analyze_root}`;
    els.rootPath.title = state.config.analyze_root;
  }

  function renderBanner() {
    const running = getActiveScan();
    if (!running) {
      els.scanBanner.hidden = true;
      els.scanBannerSummary.textContent = "";
      els.scanBannerDetails.textContent = "";
      delete els.scanBanner.dataset.state;
      return;
    }

    const progress = running.progress || null;
    const currentPath = progress?.current_path || state.config?.analyze_root || "-";
    const scannedBytes = App.formatBytes(progress?.scanned_bytes || 0);
    const elapsed = running.started_at ? App.formatElapsed(running.started_at) : "just started";
    const scannedNodes = progress?.scanned_nodes ?? 0;

    els.scanBanner.hidden = false;
    els.scanBanner.dataset.state = running.status;

    if (running.status === "queued") {
      els.scanBannerSummary.textContent = `Scan #${running.id} is queued`;
      els.scanBannerDetails.textContent = `Waiting to scan ${App.shortPath(currentPath)}`;
      return;
    }

    els.scanBannerSummary.textContent = `Scanning ${scannedNodes} items and ${scannedBytes}`;
    els.scanBannerDetails.textContent = `Current: ${App.shortPath(currentPath)} | Elapsed: ${elapsed}`;
  }

  function renderAlert() {
    if (!state.alert) {
      els.appAlert.hidden = true;
      els.appAlertText.textContent = "";
      els.alertRetryButton.hidden = true;
      return;
    }

    els.appAlert.hidden = false;
    els.appAlertText.textContent = state.alert.message;
    els.alertRetryButton.hidden = !state.alert.retry;
    els.alertRetryButton.disabled = App.isScanActive(getActiveScan());
  }

  function renderSummary() {
    const latestCompleted = getLatestCompletedScan();
    const active = getActiveScan();

    els.summaryLatest.textContent = latestCompleted
      ? `#${latestCompleted.id} • ${App.buildScanSummaryText(latestCompleted)}`
      : "No completed scans";
    els.summarySaved.textContent = `${state.historyItems.length}`;
    els.summaryActive.textContent = active
      ? `#${active.id} • ${active.status}`
      : "No scan running";
    els.summaryCompare.textContent = state.compareOpen
      ? compareSummaryLabel()
      : "Closed";

    App.renderStatusChip(els.scanState, active || latestCompleted || null);
  }

  function renderHeroActions() {
    els.openLatestButton.disabled = !getLatestCompletedScan();
    els.runScanButton.disabled = App.isScanActive(getActiveScan());
    els.toggleCompareButton.textContent = state.compareOpen ? "Hide compare" : "Compare scans";
    els.toggleCompareButton.setAttribute("aria-expanded", state.compareOpen ? "true" : "false");
  }

  function renderHistory() {
    App.clearChildren(els.historyList);

    if (!state.historyItems.length) {
      els.historySummary.textContent = "No scans loaded yet.";
      els.historyEmpty.hidden = false;
      els.historyEmpty.textContent = "Run a scan to build the archive.";
      return;
    }

    els.historySummary.textContent = `${state.historyItems.length} scans saved, newest first`;
    els.historyEmpty.hidden = true;

    state.historyItems.forEach((scan) => {
      const row = document.createElement("div");
      row.className = `history-row${scan.id === state.compareTargetId ? " active" : ""}`;

      const top = document.createElement("div");
      top.className = "history-top";

      const main = document.createElement("div");
      main.className = "history-main";

      const id = document.createElement("div");
      id.className = "history-id";
      id.textContent = `#${scan.id}`;

      const meta = document.createElement("div");
      meta.className = "history-meta";
      meta.textContent = App.buildArchiveMeta(scan);

      const status = document.createElement("span");
      status.className = `history-status ${scan.status}`;
      status.textContent = scan.status;

      const size = document.createElement("div");
      size.className = "history-size";
      size.textContent = App.formatBytes(scan.total_bytes || 0);

      main.append(id, meta);
      top.append(main, status, size);

      const actions = document.createElement("div");
      actions.className = "history-actions";

      const openButton = document.createElement("button");
      openButton.type = "button";
      openButton.className = "history-button";
      openButton.textContent = "Open";
      openButton.addEventListener("click", () => {
        window.location.href = App.buildExploreHref(scan.id, state.config?.analyze_root || null);
      });
      actions.appendChild(openButton);

      if (scan.status !== "running" && scan.status !== "queued") {
        const deleteButton = document.createElement("button");
        deleteButton.type = "button";
        deleteButton.className = "history-button-delete";
        deleteButton.textContent = "Delete";
        deleteButton.addEventListener("click", () => {
          deleteScan(scan.id).catch((err) => showAlert(`Could not delete scan #${scan.id}: ${err.message}`));
        });
        actions.appendChild(deleteButton);
      }

      row.append(top, actions);
      els.historyList.appendChild(row);
    });
  }

  function renderCompare() {
    els.compareSection.hidden = !state.compareOpen;
    if (!state.compareOpen) {
      App.clearChildren(els.compareBreadcrumb);
      App.clearChildren(els.compareList);
      els.compareSummaryStrip.hidden = true;
      els.compareList.hidden = true;
      els.compareEmpty.hidden = true;
      return;
    }

    renderCompareSelectors();

    if (completedScans().length < 2) {
      showCompareEmpty("At least two completed scans are required to compare.");
      return;
    }

    if (!state.compareBaseId || !state.compareTargetId) {
      showCompareEmpty("Select two completed scans to compare.");
      return;
    }

    if (state.compareBaseId === state.compareTargetId) {
      showCompareEmpty("Choose two different scans.");
      return;
    }

    if (state.compareLoading) {
      showCompareEmpty("Loading comparison...");
      return;
    }

    if (!state.compareResult) {
      showCompareEmpty("Select two completed scans to compare.");
      return;
    }

    const parts = App.buildBreadcrumb(state.config?.analyze_root, state.compareResult.path);
    App.renderBreadcrumb(els.compareBreadcrumb, parts, navigateComparePath);

    const summary = state.compareResult.summary || {};
    els.compareBefore.textContent = App.formatOptionalBytes(summary.before_exists, summary.before_bytes);
    els.compareAfter.textContent = App.formatOptionalBytes(summary.after_exists, summary.after_bytes);
    els.compareDelta.textContent = App.formatSignedBytes(summary.delta_bytes || 0);
    els.compareChanged.textContent = `${(state.compareResult.items || []).length}`;
    els.compareSummaryStrip.hidden = false;

    if (!(state.compareResult.items || []).length) {
      showCompareEmpty("No changed items at this path.");
      return;
    }

    els.compareIntro.textContent = `Base #${state.compareBaseId} against target #${state.compareTargetId}`;
    els.compareEmpty.hidden = true;
    els.compareList.hidden = false;
    App.renderCompareItems(els.compareList, state.compareResult.items || [], navigateComparePath);
  }

  function renderCompareSelectors() {
    const completed = completedScans();
    const previousBase = String(state.compareBaseId || "");
    const previousTarget = String(state.compareTargetId || "");

    App.clearChildren(els.baseScanSelect);
    App.clearChildren(els.targetScanSelect);

    const basePlaceholder = document.createElement("option");
    basePlaceholder.value = "";
    basePlaceholder.textContent = "Select base scan";
    els.baseScanSelect.appendChild(basePlaceholder);

    const targetPlaceholder = document.createElement("option");
    targetPlaceholder.value = "";
    targetPlaceholder.textContent = "Select target scan";
    els.targetScanSelect.appendChild(targetPlaceholder);

    completed.forEach((scan) => {
      const baseOption = document.createElement("option");
      baseOption.value = String(scan.id);
      baseOption.textContent = `#${scan.id} (${App.formatBytes(scan.total_bytes || 0)})`;
      els.baseScanSelect.appendChild(baseOption);

      const targetOption = document.createElement("option");
      targetOption.value = String(scan.id);
      targetOption.textContent = `#${scan.id} (${App.formatBytes(scan.total_bytes || 0)})`;
      els.targetScanSelect.appendChild(targetOption);
    });

    if (completed.some((scan) => String(scan.id) === previousBase)) {
      els.baseScanSelect.value = previousBase;
    } else {
      state.compareBaseId = null;
      els.baseScanSelect.value = "";
    }

    if (completed.some((scan) => String(scan.id) === previousTarget)) {
      els.targetScanSelect.value = previousTarget;
    } else {
      state.compareTargetId = null;
      els.targetScanSelect.value = "";
    }
  }

  function showCompareEmpty(message) {
    App.clearChildren(els.compareBreadcrumb);
    App.clearChildren(els.compareList);
    els.compareSummaryStrip.hidden = true;
    els.compareList.hidden = true;
    els.compareEmpty.hidden = false;
    els.compareEmpty.textContent = message;
  }

  async function handleCompareSelectionChange() {
    state.compareBaseId = App.parsePositiveInt(els.baseScanSelect.value);
    state.compareTargetId = App.parsePositiveInt(els.targetScanSelect.value);
    state.comparePath = state.config?.analyze_root || state.comparePath;
    state.compareResult = null;
    renderCompare();
    syncUrlState();

    if (!canCompare()) {
      return;
    }

    try {
      await loadCompare(state.comparePath);
    } catch (err) {
      showAlert(`Could not load comparison: ${err.message}`);
    }
  }

  async function loadCompare(path) {
    if (!canCompare()) {
      return;
    }

    state.compareLoading = true;
    state.comparePath = path;
    renderCompare();

    try {
      const query = new URLSearchParams({
        base_scan_id: String(state.compareBaseId),
        path,
        limit: "150",
        sort: "delta_desc",
      });
      const diff = await App.apiGet(`/api/v1/scans/${state.compareTargetId}/diff?${query.toString()}`);
      state.compareResult = diff;
      state.comparePath = diff.path;
      state.compareLoading = false;
      renderCompare();
      syncUrlState();
    } catch (err) {
      state.compareLoading = false;
      state.compareResult = null;
      renderCompare();
      throw err;
    }
  }

  async function runScan() {
    if (App.isScanActive(getActiveScan())) {
      return;
    }

    state.alert = null;
    renderAll();

    try {
      const result = await App.apiPost("/api/v1/scans");
      const scan = await App.apiGet(`/api/v1/scans/${result.scan_id}`);
      upsertHistoryScan(scan);
      renderAll();
      startPolling(result.scan_id);
    } catch (err) {
      showAlert(`Could not start a new scan: ${err.message}`, true);
    }
  }

  function openLatestScan() {
    const latest = getLatestCompletedScan();
    if (!latest) {
      return;
    }
    window.location.href = App.buildExploreHref(latest.id, state.config?.analyze_root || null);
  }

  async function loadHistory() {
    const payload = await App.apiGet("/api/v1/scans?limit=200");
    state.historyItems = (payload.items || []).slice().sort((a, b) => b.id - a.id);
    renderAll();

    const active = getActiveScan();
    if (active) {
      startPolling(active.id);
      return;
    }

    clearPolling();
  }

  async function deleteScan(scanId) {
    const confirmed = window.confirm(`Delete scan #${scanId}? This cannot be undone.`);
    if (!confirmed) {
      return;
    }

    await App.apiDelete(`/api/v1/scans/${scanId}`);
    state.historyItems = state.historyItems.filter((item) => item.id !== scanId);
    if (state.compareBaseId === scanId) {
      state.compareBaseId = null;
    }
    if (state.compareTargetId === scanId) {
      state.compareTargetId = null;
    }
    if (!canCompare()) {
      state.compareResult = null;
    } else if (state.compareOpen) {
      await loadCompare(state.comparePath || state.config?.analyze_root);
    }
    renderAll();
    syncUrlState();
  }

  function startPolling(scanId) {
    if (state.pollingScanId === scanId && state.pollingHandle) {
      return;
    }

    clearPolling();
    state.pollingScanId = scanId;

    const poll = async () => {
      try {
        const scan = await App.apiGet(`/api/v1/scans/${scanId}`);
        upsertHistoryScan(scan);
        renderAll();

        if (scan.status === "completed" || scan.status === "failed") {
          clearPolling();
          App.logScanWarnings(scan);
          if (state.compareOpen && canCompare()) {
            await loadCompare(state.comparePath || state.config?.analyze_root);
          }
          return;
        }

        state.pollingHandle = window.setTimeout(poll, 900);
      } catch (err) {
        clearPolling();
        showAlert(`Lost connection while polling scan progress: ${err.message}`, true);
      }
    };

    poll().catch((err) => {
      clearPolling();
      showAlert(`Could not poll scan progress: ${err.message}`, true);
    });
  }

  function upsertHistoryScan(scan) {
    const index = state.historyItems.findIndex((item) => item.id === scan.id);
    if (index >= 0) {
      state.historyItems[index] = scan;
    } else {
      state.historyItems.unshift(scan);
    }
    state.historyItems.sort((a, b) => b.id - a.id);
  }

  function navigateComparePath(path, label) {
    loadCompare(path).catch((err) => showAlert(`Could not open ${label || App.basename(path)}: ${err.message}`));
  }

  function showAlert(message, retry = false) {
    state.alert = { message, retry };
    renderAlert();
  }

  function syncUrlState() {
    App.replaceUrl("/history", {
      base_scan: state.compareOpen ? state.compareBaseId || null : null,
      target_scan: state.compareOpen ? state.compareTargetId || null : null,
      path: state.compareOpen && canCompare() ? state.comparePath || null : null,
    });
  }

  function clearPolling() {
    if (!state.pollingHandle) {
      state.pollingScanId = null;
      return;
    }
    clearTimeout(state.pollingHandle);
    state.pollingHandle = null;
    state.pollingScanId = null;
  }

  function getLatestCompletedScan() {
    return state.historyItems.find((item) => item.status === "completed") || null;
  }

  function getActiveScan() {
    return state.historyItems.find((item) => item.status === "running" || item.status === "queued") || null;
  }

  function completedScans() {
    return state.historyItems.filter((item) => item.status === "completed");
  }

  function canCompare() {
    return Boolean(
      state.compareBaseId &&
      state.compareTargetId &&
      state.compareBaseId !== state.compareTargetId,
    );
  }

  function compareSummaryLabel() {
    if (!canCompare()) {
      return "Awaiting selection";
    }
    const pathLabel = App.basename(state.comparePath || state.config?.analyze_root) || "root";
    return `#${state.compareBaseId} -> #${state.compareTargetId} at ${pathLabel}`;
  }
})();
