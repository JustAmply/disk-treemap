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
    summaryCaption: document.getElementById("summaryCaption"),
    summarySize: document.getElementById("summarySize"),
    summaryShownStat: document.getElementById("summaryShownStat"),
    summaryShownLabel: document.getElementById("summaryShownLabel"),
    summaryShown: document.getElementById("summaryShown"),
    summaryItems: document.getElementById("summaryItems"),
    summaryScan: document.getElementById("summaryScan"),
    appAlert: document.getElementById("appAlert"),
    appAlertText: document.getElementById("appAlertText"),
    clearFiltersButton: document.getElementById("clearFiltersButton"),
    searchInput: document.getElementById("searchInput"),
    typeFilter: document.getElementById("typeFilter"),
    minSizeInput: document.getElementById("minSizeInput"),
    sortSelect: document.getElementById("sortSelect"),
    goUpButton: document.getElementById("goUpButton"),
    resultNote: document.getElementById("resultNote"),
    chartFrame: document.getElementById("chartFrame"),
    chartEmpty: document.getElementById("chartEmpty"),
    emptyStateTitle: document.getElementById("emptyStateTitle"),
    emptyStateBody: document.getElementById("emptyStateBody"),
    chartMessage: document.getElementById("chartMessage"),
    chart: document.getElementById("chart"),
    inspectorMeta: document.getElementById("inspectorMeta"),
    breadcrumb: document.getElementById("breadcrumb"),
    selectedCard: document.getElementById("selectedCard"),
    selectedTitle: document.getElementById("selectedTitle"),
    selectedMeta: document.getElementById("selectedMeta"),
    selectedPath: document.getElementById("selectedPath"),
    selectedAction: document.getElementById("selectedAction"),
    detailTitle: document.getElementById("detailTitle"),
    detailSummary: document.getElementById("detailSummary"),
    detailScroll: document.getElementById("detailScroll"),
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
    activeItem: null,
    pathLoading: false,
    alert: null,
    pollingHandle: null,
    resizeHandle: null,
    resizeObserver: null,
    viewAbortController: null,
    detailScrollTop: 0,
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
    bindResizeObserver();
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

    if (App.isScanActive(state.currentScan)) {
      startPolling(state.currentScan.id);
    }

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
      if (event.key !== "Enter") {
        return;
      }
      event.preventDefault();
      applyFiltersFromInputs().catch((err) => showAlert(`Could not apply filters: ${err.message}`));
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
    els.goUpButton.addEventListener("click", navigateToParent);
    els.selectedAction.addEventListener("click", () => {
      if (state.activeItem?.type === "dir" && state.activeItem.path) {
        navigateToPath(state.activeItem.path, state.activeItem.name);
      }
    });
    els.detailList.addEventListener("scroll", () => {
      state.detailScrollTop = els.detailList.scrollTop;
      syncDetailScrollState();
    });
    window.addEventListener("resize", scheduleTreemapRender);
  }

  function bindResizeObserver() {
    if (!window.ResizeObserver) {
      return;
    }
    state.resizeObserver = new ResizeObserver(() => scheduleTreemapRender());
    state.resizeObserver.observe(els.chartFrame);
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
    renderGoUpState();
    renderBreadcrumb();
    renderSelectedCard();
    renderDetailList();
    renderChartArea();
    App.renderStatusChip(els.scanState, state.currentScan || state.latestCompletedScan || null);
  }

  function renderRootPath() {
    if (!state.config?.analyze_root) {
      els.rootPath.textContent = "Loading root path...";
      return;
    }

    els.rootPath.textContent = state.config.analyze_root;
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

    els.scanBannerSummary.textContent = `Scanning ${App.formatCount(scannedNodes)} items and ${scannedBytes}`;
    els.scanBannerDetails.textContent = `Current: ${App.shortPath(currentPath)} | Elapsed: ${elapsed}`;
  }

  function renderSummary() {
    const activePath = state.pathLoading
      ? state.currentPath || state.currentView?.path || state.config?.analyze_root || "Not scanned yet"
      : state.currentView?.path || state.currentPath || state.config?.analyze_root || "Not scanned yet";

    let folderSize = "-";
    let shownSize = "-";
    let itemText = "-";
    let showShownSize = false;

    if (state.currentView) {
      const summary = state.currentView.summary;
      const totalBytes = Number(summary.total_bytes || 0);
      const visibleBytes = Number(summary.visible_bytes || 0);

      folderSize = App.formatBytes(totalBytes);
      shownSize = App.formatBytes(visibleBytes);
      showShownSize = visibleBytes !== totalBytes;
      const visible = App.formatCount(summary.returned_item_count || 0);
      const matching = App.formatCount(summary.matching_item_count || 0);
      itemText = summary.is_result_truncated ? `${visible} / ${matching}` : `${matching}`;
    } else if (state.latestCompletedScan?.status === "completed") {
      folderSize = App.formatBytes(state.latestCompletedScan.total_bytes || 0);
    } else if (state.currentScan?.progress) {
      folderSize = App.formatBytes(state.currentScan.progress.scanned_bytes || 0);
    }

    els.summaryPath.textContent = buildSummaryPathLabel(activePath);
    els.summaryPath.title = activePath;
    els.summaryCaption.textContent = buildSummaryCaption(activePath);
    els.summarySize.textContent = folderSize;
    els.summaryShownStat.hidden = !showShownSize;
    els.summaryShownLabel.textContent = "In view";
    els.summaryShown.textContent = shownSize;
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

  function renderGoUpState() {
    const canGoUp = Boolean(state.config?.analyze_root && state.currentPath && state.currentPath !== state.config.analyze_root);
    els.goUpButton.disabled = !canGoUp || state.pathLoading;
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

  function renderSelectedCard() {
    const selected = state.activeItem || buildFallbackSelection();
    if (!selected) {
      els.selectedCard.hidden = true;
      return;
    }

    const isSynthetic = Boolean(selected.synthetic);
    const isFolder = selected.type === "dir";
    const isCurrentFolder = selected.path && selected.path === state.currentView?.path;

    els.selectedCard.hidden = false;
    els.selectedTitle.textContent = selected.name || "Current folder";
    els.selectedMeta.textContent = buildSelectedMeta(selected);
    els.selectedPath.textContent = selected.path ? App.shortPath(selected.path) : "Grouped items in the current view";
    els.selectedPath.title = selected.path || "Grouped items in the current view";

    const canOpen = isFolder && !isSynthetic && !isCurrentFolder;
    els.selectedAction.hidden = !canOpen;
    els.selectedAction.disabled = !canOpen;
    els.selectedAction.textContent = "Open folder";
  }

  function renderDetailList() {
    App.clearChildren(els.detailList);

    if (state.pathLoading) {
      els.detailTitle.textContent = "Items in this folder";
      els.detailSummary.textContent = "Loading direct contents...";
      els.detailEmpty.hidden = false;
      els.detailEmpty.textContent = "Fetching folders and files for this path.";
      els.inspectorMeta.textContent = `Loading ${App.basename(state.currentPath) || state.currentPath}`;
      scheduleDetailScrollSync();
      return;
    }

    if (!state.currentView) {
      els.detailTitle.textContent = "Items in this folder";
      els.detailSummary.textContent = App.isScanActive(state.currentScan) ? "Scan in progress." : "No scan data yet.";
      els.detailEmpty.hidden = false;
      els.detailEmpty.textContent = App.isScanActive(state.currentScan)
        ? "Direct folder contents appear after the first completed scan."
        : "Run a scan to inspect folders and files.";
      els.inspectorMeta.textContent = "Current folder inventory.";
      scheduleDetailScrollSync();
      return;
    }

    els.detailTitle.textContent = "Items in this folder";
    els.detailSummary.textContent = buildDetailSummary(state.currentView.summary);
    els.inspectorMeta.textContent = buildInspectorMeta(state.currentView.summary, state.currentView.path);

    if (!state.currentView.items.length) {
      els.detailEmpty.hidden = false;
      els.detailEmpty.textContent = buildEmptyItemsMessage();
      scheduleDetailScrollSync();
      return;
    }

    els.detailEmpty.hidden = true;
    App.renderItemList(els.detailList, state.currentView.items, {
      activePath: state.activeItem?.path || "",
      onFocus: selectItem,
      onNavigate: navigateToPath,
    });
    scheduleDetailScrollSync(true);
  }

  function renderChartArea() {
    els.tooltip.hidden = true;

    if (state.pathLoading && !state.currentView) {
      showChartMessage("Loading folder view...");
      return;
    }

    if (state.currentView) {
      els.resultNote.textContent = buildResultNote(state.currentView.summary);

      const hasTreemapData = Array.isArray(state.currentView.treemap?.children) && state.currentView.treemap.children.length > 0;
      if (!hasTreemapData) {
        showChartMessage(buildEmptyChartMessage());
        return;
      }

      els.chartFrame.dataset.view = "chart";
      els.chartEmpty.hidden = true;
      els.chartMessage.hidden = true;
      els.chart.hidden = false;
      App.renderTreemap(els.chart, els.tooltip, state.currentView.treemap, {
        activePath: state.activeItem?.path || "",
        onFocus: selectItem,
        onNavigate: navigateToPath,
      });
      return;
    }

    els.resultNote.textContent = "Run a scan to load the first interactive treemap.";

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
      "Scan the configured root path to build a clickable treemap of the current folder contents.",
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
    abortViewLoad();
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
      state.activeItem = null;
      renderAll();
      syncUrlState();
      if (App.isScanActive(selected)) {
        startPolling(selected.id);
      }
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
          state.currentPath = state.currentView?.path || state.config?.analyze_root || state.currentPath;
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

    abortViewLoad();

    state.currentPath = path;
    state.pathLoading = true;
    state.detailScrollTop = 0;
    state.alert = null;
    renderAll();

    const controller = new AbortController();
    state.viewAbortController = controller;

    try {
      const query = new URLSearchParams({
        path,
        limit: String(state.config?.max_children_per_query || 500),
        q: state.filters.q,
        type: state.filters.type,
        min_size: String(state.filters.minSize),
        sort: state.filters.sort,
      });

      const response = await App.apiGet(`/api/v1/scans/${state.viewedScanId}/explore?${query.toString()}`, {
        signal: controller.signal,
      });

      if (state.viewAbortController !== controller) {
        return;
      }

      state.currentView = response;
      state.currentPath = response.path;
      state.activeItem = chooseNextActiveItem(response, state.activeItem?.path);
      state.pathLoading = false;
      renderAll();
      syncUrlState();
    } catch (err) {
      if (err.name === "AbortError") {
        return;
      }
      state.pathLoading = false;
      renderAll();
      throw err;
    } finally {
      if (state.viewAbortController === controller) {
        state.viewAbortController = null;
      }
    }
  }

  function chooseNextActiveItem(view, preferredPath) {
    if (!view) {
      return null;
    }
    if (preferredPath) {
      const match = (view.items || []).find((item) => item.path === preferredPath);
      if (match) {
        return match;
      }
    }
    if (view.items?.length) {
      return view.items[0];
    }
    return buildFallbackSelection(view);
  }

  function buildFallbackSelection(view = state.currentView) {
    if (!view) {
      return null;
    }
    return {
      name: App.basename(view.path) || view.path,
      path: view.path,
      type: "dir",
      size_bytes: view.summary?.total_bytes || 0,
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

  function selectItem(item) {
    if (!item) {
      return;
    }
    state.activeItem = item;
    renderSelectedCard();
    renderDetailList();
    scheduleTreemapRender();
  }

  function navigateToPath(path, label) {
    loadPath(path).catch((err) => showAlert(`Could not open ${label || App.basename(path)}: ${err.message}`));
  }

  function navigateToParent() {
    if (!state.config?.analyze_root || !state.currentPath || state.currentPath === state.config.analyze_root) {
      return;
    }

    const parts = App.buildBreadcrumb(state.config.analyze_root, state.currentPath);
    if (parts.length < 2) {
      return;
    }
    const parent = parts[parts.length - 2];
    navigateToPath(parent.path, parent.label);
  }

  function scheduleTreemapRender() {
    if (!state.currentView || state.pathLoading) {
      return;
    }

    clearTimeout(state.resizeHandle);
    state.resizeHandle = window.setTimeout(() => {
      if (state.currentView && !state.pathLoading) {
        App.renderTreemap(els.chart, els.tooltip, state.currentView.treemap, {
          activePath: state.activeItem?.path || "",
          onFocus: selectItem,
          onNavigate: navigateToPath,
        });
      }
    }, 120);
  }

  function buildDetailSummary(summary) {
    const sortLabel = App.sortOptions.find((option) => option.value === state.filters.sort)?.label || "Largest first";
    const items = App.formatCount(summary.matching_item_count || 0);
    const dirs = App.formatCount(summary.visible_dir_count || 0);
    const files = App.formatCount(summary.visible_file_count || 0);
    const base = `${items} matching items • ${dirs} folders • ${files} files • ${sortLabel}`;

    if (!summary.is_result_truncated) {
      return base;
    }

    return `${base} • showing ${App.formatCount(summary.returned_item_count || 0)} rows`;
  }

  function buildInspectorMeta(summary, path) {
    const shown = App.formatBytes(summary.visible_bytes || 0);
    const total = App.formatBytes(summary.total_bytes || 0);
    return `Direct contents of ${App.basename(path) || path} • ${shown} in view of ${total}`;
  }

  function buildSelectedMeta(item) {
    if (item.synthetic) {
      return `${App.formatCount(item.hidden_item_count || 0)} grouped items • ${App.formatBytes(item.size_bytes || 0)}`;
    }
    const typeLabel = item.type === "dir" ? "Folder" : "File";
    return `${typeLabel} • ${App.formatBytes(item.size_bytes || 0)}`;
  }

  function buildSummaryPathLabel(activePath) {
    const root = state.config?.analyze_root || "";
    if (!activePath) {
      return "Not scanned yet";
    }
    if (!root) {
      return activePath;
    }

    const normalizedRoot = root.replace(/[\\/]+$/, "");
    const normalizedPath = activePath.replace(/[\\/]+$/, "");
    if (normalizedPath === normalizedRoot) {
      return "Root folder";
    }
    if (!normalizedPath.startsWith(normalizedRoot)) {
      return activePath;
    }

    const relativePath = normalizedPath.slice(normalizedRoot.length).replace(/^[/\\]+/, "");
    return relativePath || "Root folder";
  }

  function buildSummaryCaption(activePath) {
    if (state.pathLoading) {
      return "Loading direct contents for this folder.";
    }

    if (!state.currentView) {
      if (App.isScanActive(state.currentScan)) {
        return "A scan is running. This summary will fill in when the first view is ready.";
      }
      return "Run a scan to inspect the configured root.";
    }

    const summary = state.currentView.summary;
    if (summary.has_active_filters) {
      return summary.is_result_truncated
        ? "Filtered view with the largest matching direct entries."
        : "Filtered view of the matching direct entries.";
    }
    if (summary.is_result_truncated) {
      return "Showing the largest direct entries in this folder.";
    }
    if (activePath === state.config?.analyze_root) {
      return "Configured root folder.";
    }
    return "Direct contents of the selected folder.";
  }

  function buildResultNote(summary) {
    const shown = App.formatCount(summary.returned_item_count || 0);
    const matching = App.formatCount(summary.matching_item_count || 0);
    const visibleBytes = App.formatBytes(summary.visible_bytes || 0);
    const totalBytes = App.formatBytes(summary.total_bytes || 0);

    if (summary.has_active_filters) {
      return `Filtered view: ${matching} matching items, ${visibleBytes} represented in the treemap.`;
    }
    if (summary.is_result_truncated) {
      return `Showing ${shown} of ${matching} direct items. Refine the filters to isolate smaller entries faster.`;
    }
    return `Showing the full folder inventory: ${matching} direct items, ${totalBytes} total.`;
  }

  function buildEmptyItemsMessage() {
    if (filtersAreActive()) {
      return "No folders or files match the current filters at this path.";
    }
    return "This folder has no direct files or subfolders.";
  }

  function buildEmptyChartMessage() {
    if (filtersAreActive()) {
      return "No treemap tiles match the current filters at this path.";
    }
    return "This folder has no direct files or subfolders to visualize.";
  }

  function showAlert(message, retry = false) {
    state.alert = { message, retry };
    renderAlert();
  }

  function scheduleDetailScrollSync(restoreScroll = false) {
    window.requestAnimationFrame(() => {
      if (restoreScroll) {
        const maxScroll = Math.max(els.detailList.scrollHeight - els.detailList.clientHeight, 0);
        els.detailList.scrollTop = Math.min(state.detailScrollTop, maxScroll);
      }
      syncDetailScrollState();
    });
  }

  function syncDetailScrollState() {
    const maxScroll = Math.max(els.detailList.scrollHeight - els.detailList.clientHeight, 0);
    const isScrollable = maxScroll > 6;
    const atTop = !isScrollable || els.detailList.scrollTop <= 2;
    const atBottom = !isScrollable || els.detailList.scrollTop >= maxScroll - 2;

    els.detailScroll.dataset.scrollable = isScrollable ? "true" : "false";
    els.detailScroll.dataset.atTop = atTop ? "true" : "false";
    els.detailScroll.dataset.atBottom = atBottom ? "true" : "false";
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

  function abortViewLoad() {
    if (!state.viewAbortController) {
      return;
    }
    state.viewAbortController.abort();
    state.viewAbortController = null;
  }
})();
