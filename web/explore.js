(() => {
  const App = window.DiskTreemapApp;

  const els = {
    rootPath: document.getElementById("rootPath"),
    scanButton: document.getElementById("scanButton"),
    emptyScanButton: document.getElementById("emptyScanButton"),
    alertRetryButton: document.getElementById("alertRetryButton"),
    scanState: document.getElementById("scanState"),
    scanBanner: document.getElementById("scanBanner"),
    scanBannerSummary: document.getElementById("scanBannerSummary"),
    scanBannerDetails: document.getElementById("scanBannerDetails"),
    summaryPath: document.getElementById("summaryPath"),
    summarySize: document.getElementById("summarySize"),
    summaryItems: document.getElementById("summaryItems"),
    summaryScan: document.getElementById("summaryScan"),
    appAlert: document.getElementById("appAlert"),
    appAlertText: document.getElementById("appAlertText"),
    clearFiltersButton: document.getElementById("clearFiltersButton"),
    searchInput: document.getElementById("searchInput"),
    typeFilter: document.getElementById("typeFilter"),
    minSizeInput: document.getElementById("minSizeInput"),
    sortSelect: document.getElementById("sortSelect"),
    chartFrame: document.getElementById("chartFrame"),
    chartEmpty: document.getElementById("chartEmpty"),
    emptyStateTitle: document.getElementById("emptyStateTitle"),
    emptyStateBody: document.getElementById("emptyStateBody"),
    chartMessage: document.getElementById("chartMessage"),
    chart: document.getElementById("chart"),
    inspectorMeta: document.getElementById("inspectorMeta"),
    breadcrumb: document.getElementById("breadcrumb"),
    detailTitle: document.getElementById("detailTitle"),
    detailSummary: document.getElementById("detailSummary"),
    detailList: document.getElementById("detailList"),
    detailEmpty: document.getElementById("detailEmpty"),
    tooltip: document.getElementById("tooltip"),
  };

  const defaultFilters = {
    q: "",
    type: "",
    minSize: 0,
    sort: "size_desc",
  };

  const state = {
    config: null,
    currentScan: null,
    latestCompletedScan: null,
    viewedScanId: null,
    currentPath: null,
    currentView: null,
    pathLoading: false,
    alert: null,
    pollingHandle: null,
    resizeHandle: null,
    filters: { ...defaultFilters },
    urlState: App.readExploreUrlState(),
  };

  const queueSearchApply = App.debounce(() => {
    applyFiltersFromInputs().catch((err) => showAlert(`Could not apply filters: ${err.message}`));
  }, 220);

  init().catch((err) => {
    state.alert = {
      message: `Unable to load the app: ${err.message}`,
      retry: false,
    };
    App.renderStatusChip(els.scanState, { status: "failed" });
    renderAll();
  });

  async function init() {
    bindEvents();
    syncSortOptions();
    syncFilterInputs();
    renderAll();

    const cfg = await App.apiGet("/api/v1/config");
    state.config = cfg;
    state.currentScan = cfg.current_scan || null;
    state.latestCompletedScan = cfg.latest_completed_scan || null;
    state.currentPath = state.urlState.path || cfg.analyze_root;
    state.filters = {
      q: state.urlState.q || "",
      type: state.urlState.type || "",
      minSize: state.urlState.minSize || 0,
      sort: state.urlState.sort || defaultFilters.sort,
    };

    syncFilterInputs();
    renderAll();

    const desiredScanId = chooseInitialViewScanId();
    if (!desiredScanId) {
      return;
    }

    await openViewScan(desiredScanId, { preservePath: true, fallbackOnMissing: true });
  }

  function bindEvents() {
    els.scanButton.addEventListener("click", runScan);
    els.emptyScanButton.addEventListener("click", runScan);
    els.alertRetryButton.addEventListener("click", runScan);
    els.clearFiltersButton.addEventListener("click", () => {
      resetFilters().catch((err) => showAlert(`Could not reset filters: ${err.message}`));
    });
    els.searchInput.addEventListener("input", () => {
      queueSearchApply();
    });
    els.searchInput.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        applyFiltersFromInputs().catch((err) => showAlert(`Could not apply filters: ${err.message}`));
      }
    });
    els.typeFilter.addEventListener("change", () => {
      applyFiltersFromInputs().catch((err) => showAlert(`Could not apply filters: ${err.message}`));
    });
    els.minSizeInput.addEventListener("change", () => {
      applyFiltersFromInputs().catch((err) => showAlert(`Could not apply filters: ${err.message}`));
    });
    els.sortSelect.addEventListener("change", () => {
      applyFiltersFromInputs().catch((err) => showAlert(`Could not apply filters: ${err.message}`));
    });
    window.addEventListener("resize", scheduleTreemapRender);
  }

  function chooseInitialViewScanId() {
    if (state.urlState.scanId) {
      return state.urlState.scanId;
    }
    if (state.currentScan?.status === "completed") {
      return state.currentScan.id;
    }
    if (state.latestCompletedScan?.id) {
      return state.latestCompletedScan.id;
    }
    return null;
  }

  function renderAll() {
    renderRootPath();
    renderBanner();
    renderSummary();
    renderAlert();
    renderToolbarState();
    renderBreadcrumb();
    renderDetailList();
    renderChartArea();
    App.renderStatusChip(els.scanState, state.currentScan || state.latestCompletedScan || null);
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
    const scan = state.currentScan;
    if (!scan || !App.isScanActive(scan)) {
      els.scanBanner.hidden = true;
      els.scanBannerSummary.textContent = "";
      els.scanBannerDetails.textContent = "";
      delete els.scanBanner.dataset.state;
      return;
    }

    const progress = scan.progress || null;
    const currentPath = progress?.current_path || state.config?.analyze_root || "-";
    const scannedBytes = App.formatBytes(progress?.scanned_bytes || 0);
    const elapsed = scan.started_at ? App.formatElapsed(scan.started_at) : "just started";
    const scannedNodes = progress?.scanned_nodes ?? 0;

    els.scanBanner.hidden = false;
    els.scanBanner.dataset.state = scan.status;

    if (scan.status === "queued") {
      els.scanBannerSummary.textContent = `Scan #${scan.id ?? "-"} is queued`;
      els.scanBannerDetails.textContent = `Waiting for progress updates from ${App.shortPath(currentPath)}`;
      return;
    }

    els.scanBannerSummary.textContent = `Scanning ${scannedNodes} items and ${scannedBytes}`;
    els.scanBannerDetails.textContent = `Current: ${App.shortPath(currentPath)} | Elapsed: ${elapsed}`;
  }

  function renderSummary() {
    const activePath = state.pathLoading
      ? state.currentPath || state.currentView?.path || state.config?.analyze_root || "Not scanned yet"
      : state.currentView?.path || state.currentPath || state.config?.analyze_root || "Not scanned yet";

    let size = "-";
    if (state.currentView) {
      size = App.formatBytes(state.currentView.totalBytes);
    } else if (state.latestCompletedScan?.status === "completed") {
      size = App.formatBytes(state.latestCompletedScan.total_bytes || 0);
    } else if (state.currentScan?.progress) {
      size = App.formatBytes(state.currentScan.progress.scanned_bytes || 0);
    }

    let itemText = "-";
    if (state.currentView) {
      itemText = `${state.currentView.itemCount} visible`;
    } else if (state.currentScan?.progress) {
      itemText = `${state.currentScan.progress.scanned_nodes} scanned`;
    } else if (state.latestCompletedScan?.status === "completed") {
      itemText = `${state.latestCompletedScan.total_nodes} total`;
    }

    els.summaryPath.textContent = activePath;
    els.summaryPath.title = activePath;
    els.summarySize.textContent = size;
    els.summaryItems.textContent = itemText;
    els.summaryScan.textContent = App.buildScanSummaryText(state.currentScan || state.latestCompletedScan);
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
    els.alertRetryButton.disabled = App.isScanActive(state.currentScan);
  }

  function renderToolbarState() {
    els.clearFiltersButton.hidden = !filtersAreActive();
  }

  function renderBreadcrumb() {
    if (!state.config?.analyze_root) {
      App.clearChildren(els.breadcrumb);
      return;
    }

    const path = state.pathLoading
      ? state.currentPath || state.currentView?.path || state.config.analyze_root
      : state.currentView?.path || state.currentPath || state.config.analyze_root;
    const parts = App.buildBreadcrumb(state.config.analyze_root, path);
    App.renderBreadcrumb(els.breadcrumb, parts, navigateToPath);
  }

  function renderDetailList() {
    App.clearChildren(els.detailList);

    if (state.pathLoading) {
      els.detailTitle.textContent = "Largest items";
      els.detailSummary.textContent = "Loading items...";
      els.detailEmpty.hidden = false;
      els.detailEmpty.textContent = "Fetching items for this folder.";
      els.inspectorMeta.textContent = `Loading items from ${App.basename(state.currentPath) || state.currentPath}`;
      return;
    }

    if (!state.currentView) {
      els.detailTitle.textContent = "Largest items";
      els.detailSummary.textContent = App.isScanActive(state.currentScan) ? "Scan in progress." : "No scan data yet.";
      els.detailEmpty.hidden = false;
      els.detailEmpty.textContent = App.isScanActive(state.currentScan)
        ? "The ranked item list will appear after the first completed scan."
        : "Run a scan to inspect folders and files.";
      els.inspectorMeta.textContent = "Largest items in the current view.";
      return;
    }

    els.detailTitle.textContent = state.currentView.detailTitle;
    els.detailSummary.textContent = state.currentView.detailSummary;
    els.inspectorMeta.textContent = state.currentView.inspectorMeta;

    if (!state.currentView.detailItems.length) {
      els.detailEmpty.hidden = false;
      els.detailEmpty.textContent = state.currentView.emptyDetailMessage;
      return;
    }

    els.detailEmpty.hidden = true;
    App.renderLargestItems(els.detailList, state.currentView.detailItems, navigateToPath);
  }

  function renderChartArea() {
    els.tooltip.hidden = true;

    if (state.pathLoading && !state.currentView) {
      showChartMessage("Loading folder view...");
      return;
    }

    if (state.currentView) {
      if (!state.currentView.chartItems.length) {
        showChartMessage(state.currentView.emptyChartMessage);
        return;
      }

      els.chartFrame.dataset.view = "chart";
      els.chartEmpty.hidden = true;
      els.chartMessage.hidden = true;
      els.chart.hidden = false;
      App.renderTreemap(els.chart, els.tooltip, state.currentView, navigateToPath);
      return;
    }

    if (App.isScanActive(state.currentScan)) {
      showEmptyState(
        "Scan in progress",
        "The treemap will appear here as soon as the first completed scan is ready.",
        false,
      );
      return;
    }

    if (state.currentScan?.status === "failed") {
      showEmptyState(
        "Scan failed",
        "Run another scan to rebuild the treemap for this root path.",
        true,
      );
      return;
    }

    showEmptyState(
      "Run a scan to map this folder",
      "Scan the configured root path to build a treemap and inspect the current scan in context.",
      true,
    );
  }

  function showEmptyState(title, body, showAction) {
    els.chartFrame.dataset.view = "empty";
    els.emptyStateTitle.textContent = title;
    els.emptyStateBody.textContent = body;
    els.chartEmpty.hidden = false;
    els.chartMessage.hidden = true;
    els.chart.hidden = true;
    els.emptyScanButton.hidden = !showAction;
    els.emptyScanButton.disabled = App.isScanActive(state.currentScan);
    App.clearChildren(els.chart);
  }

  function showChartMessage(message) {
    els.chartFrame.dataset.view = "message";
    els.chartEmpty.hidden = true;
    els.chartMessage.hidden = false;
    els.chartMessage.textContent = message;
    els.chart.hidden = true;
    App.clearChildren(els.chart);
  }

  async function runScan() {
    if (App.isScanActive(state.currentScan)) {
      return;
    }

    clearPolling();
    state.alert = null;
    state.currentScan = {
      id: state.currentScan?.id ?? null,
      status: "queued",
      warning_count: 0,
      progress: null,
    };
    renderAll();

    try {
      const result = await App.apiPost("/api/v1/scans");
      state.currentScan = {
        id: result.scan_id,
        status: "queued",
        warning_count: 0,
        progress: null,
      };
      renderAll();
      startPolling(result.scan_id);
      syncUrlState();
    } catch (err) {
      state.currentScan = {
        ...state.currentScan,
        status: "failed",
        error: err.message,
      };
      showAlert(`Could not start a new scan: ${err.message}`, true);
      renderAll();
    }
  }

  async function openViewScan(scanId, { preservePath = false, fallbackOnMissing = false } = {}) {
    try {
      const selected = await App.apiGet(`/api/v1/scans/${encodeURIComponent(scanId)}`);
      state.viewedScanId = scanId;

      if (selected.status === "completed") {
        if (!state.latestCompletedScan || selected.id >= state.latestCompletedScan.id) {
          state.latestCompletedScan = selected;
        }
        const targetPath = preservePath && state.currentPath ? state.currentPath : state.config?.analyze_root || state.currentPath;
        await loadPath(targetPath);
        return;
      }

      if (selected.id === state.currentScan?.id || !state.currentScan) {
        state.currentScan = selected;
      }
      state.currentView = null;
      renderAll();
      syncUrlState();
    } catch (err) {
      if (!fallbackOnMissing) {
        throw err;
      }

      const fallbackScanId = state.latestCompletedScan?.id && state.latestCompletedScan.id !== scanId
        ? state.latestCompletedScan.id
        : null;
      if (!fallbackScanId) {
        showAlert(`Could not open scan #${scanId}: ${err.message}`);
        renderAll();
        return;
      }

      showAlert(`Scan #${scanId} is no longer available. Showing the latest completed scan instead.`);
      await openViewScan(fallbackScanId, { preservePath: true, fallbackOnMissing: false });
    }
  }

  function startPolling(scanId) {
    disableScanButtons(true);
    clearPolling();

    const poll = async () => {
      try {
        const scan = await App.apiGet(`/api/v1/scans/${scanId}`);
        state.currentScan = scan;
        renderAll();

        if (scan.status === "completed") {
          clearPolling();
          disableScanButtons(false);
          App.logScanWarnings(scan);
          state.latestCompletedScan = scan;
          state.viewedScanId = scan.id;
          state.currentPath = state.config?.analyze_root || state.currentPath;
          await loadPath(state.currentPath);
          return;
        }

        if (scan.status === "failed") {
          clearPolling();
          disableScanButtons(false);
          showAlert(`Scan failed: ${scan.error || "unknown error"}`, true);
          renderAll();
          return;
        }

        state.pollingHandle = window.setTimeout(poll, 900);
      } catch (err) {
        clearPolling();
        disableScanButtons(false);
        showAlert(`Lost connection while polling scan progress: ${err.message}`, true);
      }
    };

    poll().catch((err) => {
      clearPolling();
      disableScanButtons(false);
      showAlert(`Could not poll scan progress: ${err.message}`, true);
    });
  }

  async function reloadCurrentPath() {
    if (!state.viewedScanId) {
      syncUrlState();
      renderAll();
      return;
    }

    await loadPath(state.currentPath || state.config?.analyze_root);
  }

  async function loadPath(path) {
    if (!state.viewedScanId) {
      return;
    }

    state.currentPath = path;
    state.pathLoading = true;
    state.alert = null;
    renderAll();

    try {
      const query = new URLSearchParams({
        path,
        limit: "150",
        q: state.filters.q,
        type: state.filters.type,
        min_size: String(state.filters.minSize),
        sort: state.filters.sort,
      });

      const [children, largest] = await Promise.all([
        App.apiGet(`/api/v1/scans/${state.viewedScanId}/children?${query.toString()}`),
        App.apiGet(`/api/v1/scans/${state.viewedScanId}/largest?${query.toString()}`),
      ]);

      state.currentView = buildNormalView(children, largest.items || []);
      state.currentPath = children.path;
      state.pathLoading = false;
      renderAll();
      syncUrlState();
    } catch (err) {
      state.pathLoading = false;
      renderAll();
      throw err;
    }
  }

  function buildNormalView(children, largestItems) {
    const chartItems = (children.children || []).map((item) => ({
      name: item.name,
      path: item.path,
      type: item.type,
      visualValue: Math.max(item.size_bytes || 0, 1),
      colorClass: item.type === "dir" ? "dir" : "file",
      clickable: item.type === "dir",
      metaLabel: App.formatBytes(item.size_bytes || 0),
      tooltip: [
        `<strong>${App.escapeHtml(item.name)}</strong>`,
        App.escapeHtml(App.shortPath(item.path)),
        `${App.formatBytes(item.size_bytes || 0)} (${App.formatPercent(item.size_bytes || 0, children.total_bytes || 0)})`,
        App.escapeHtml(item.type),
      ].join("<br>"),
    }));

    return {
      path: children.path,
      totalBytes: children.total_bytes || 0,
      itemCount: (children.children || []).length,
      chartItems,
      emptyChartMessage: "No child items at this path for current filters.",
      detailTitle: "Largest items",
      detailSummary: `${largestItems.length} entries ranked by size`,
      emptyDetailMessage: "No items match the current filters.",
      detailItems: largestItems,
      inspectorMeta: `${largestItems.length} largest items from ${App.basename(children.path) || children.path}`,
    };
  }

  function syncSortOptions() {
    App.clearChildren(els.sortSelect);
    App.sortOptions.forEach((optionDef) => {
      const option = document.createElement("option");
      option.value = optionDef.value;
      option.textContent = optionDef.label;
      els.sortSelect.appendChild(option);
    });
  }

  function syncFilterInputs() {
    els.searchInput.value = state.filters.q;
    els.typeFilter.value = state.filters.type;
    els.minSizeInput.value = String(state.filters.minSize || 0);
    els.sortSelect.value = state.filters.sort || defaultFilters.sort;
  }

  async function applyFiltersFromInputs() {
    state.filters = {
      q: (els.searchInput.value || "").trim(),
      type: (els.typeFilter.value || "").trim(),
      minSize: Math.max(0, Number.parseInt(els.minSizeInput.value || "0", 10) || 0),
      sort: (els.sortSelect.value || "").trim() || defaultFilters.sort,
    };
    syncFilterInputs();
    await reloadCurrentPath();
  }

  async function resetFilters() {
    state.filters = { ...defaultFilters };
    syncFilterInputs();
    await reloadCurrentPath();
  }

  function filtersAreActive() {
    return Boolean(
      state.filters.q ||
      state.filters.type ||
      state.filters.minSize > 0 ||
      state.filters.sort !== defaultFilters.sort,
    );
  }

  function navigateToPath(path, label) {
    loadPath(path).catch((err) => showAlert(`Could not open ${label || App.basename(path)}: ${err.message}`));
  }

  function scheduleTreemapRender() {
    if (!state.currentView || state.pathLoading) {
      return;
    }

    clearTimeout(state.resizeHandle);
    state.resizeHandle = window.setTimeout(() => {
      if (state.currentView && !state.pathLoading) {
        App.renderTreemap(els.chart, els.tooltip, state.currentView, navigateToPath);
      }
    }, 120);
  }

  function showAlert(message, retry = false) {
    state.alert = { message, retry };
    renderAlert();
  }

  function disableScanButtons(disabled) {
    els.scanButton.disabled = disabled;
    els.emptyScanButton.disabled = disabled;
    els.alertRetryButton.disabled = disabled;
  }

  function syncUrlState() {
    App.replaceUrl("/", {
      scan: state.viewedScanId || null,
      path: state.currentPath || null,
      q: state.filters.q || null,
      type: state.filters.type || null,
      min_size: state.filters.minSize > 0 ? state.filters.minSize : null,
      sort: state.filters.sort && state.filters.sort !== defaultFilters.sort ? state.filters.sort : null,
    });
  }

  function clearPolling() {
    if (!state.pollingHandle) {
      return;
    }
    clearTimeout(state.pollingHandle);
    state.pollingHandle = null;
  }
})();
