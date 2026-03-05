(() => {
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
    compareBaseSelect: document.getElementById("compareBaseSelect"),
    clearCompareButton: document.getElementById("clearCompareButton"),
    searchInput: document.getElementById("searchInput"),
    typeFilter: document.getElementById("typeFilter"),
    minSizeInput: document.getElementById("minSizeInput"),
    sortSelect: document.getElementById("sortSelect"),
    applyFiltersButton: document.getElementById("applyFiltersButton"),
    resetFiltersButton: document.getElementById("resetFiltersButton"),
    compareMeta: document.getElementById("compareMeta"),
    compareSummary: document.getElementById("compareSummary"),
    comparePathState: document.getElementById("comparePathState"),
    chartFrame: document.getElementById("chartFrame"),
    chartEmpty: document.getElementById("chartEmpty"),
    emptyStateTitle: document.getElementById("emptyStateTitle"),
    emptyStateBody: document.getElementById("emptyStateBody"),
    chartMessage: document.getElementById("chartMessage"),
    chart: document.getElementById("chart"),
    inspectorMeta: document.getElementById("inspectorMeta"),
    breadcrumb: document.getElementById("breadcrumb"),
    historySummary: document.getElementById("historySummary"),
    refreshHistoryButton: document.getElementById("refreshHistoryButton"),
    historyList: document.getElementById("historyList"),
    historyEmpty: document.getElementById("historyEmpty"),
    detailTitle: document.getElementById("detailTitle"),
    detailSummary: document.getElementById("detailSummary"),
    detailList: document.getElementById("detailList"),
    detailEmpty: document.getElementById("detailEmpty"),
    tooltip: document.getElementById("tooltip"),
  };

  const sortOptions = {
    normal: [
      { value: "size_desc", label: "Size desc" },
      { value: "size_asc", label: "Size asc" },
      { value: "name_asc", label: "Name asc" },
      { value: "name_desc", label: "Name desc" },
    ],
    compare: [
      { value: "delta_desc", label: "Delta desc" },
      { value: "delta_asc", label: "Delta asc" },
      { value: "size_desc", label: "Size desc" },
      { value: "size_asc", label: "Size asc" },
      { value: "name_asc", label: "Name asc" },
      { value: "name_desc", label: "Name desc" },
    ],
  };

  const defaultFilters = {
    q: "",
    type: "",
    minSize: 0,
    sort: "",
  };

  const state = {
    config: null,
    activeScanId: null,
    latestScan: null,
    currentPath: null,
    currentView: null,
    historyItems: [],
    baseScanId: null,
    filters: { ...defaultFilters },
    urlState: readUrlState(),
    pollingHandle: null,
    resizeHandle: null,
    pathLoading: false,
    alert: null,
  };

  init().catch((err) => {
    state.alert = {
      message: `Unable to load the app: ${err.message}`,
      retry: false,
    };
    setStatusChip("failed", "Load failed");
    renderAll();
  });

  async function init() {
    bindEvents();
    renderAll();

    const cfg = await apiGet("/api/v1/config");
    state.config = cfg;
    state.currentPath = state.urlState.path || cfg.analyze_root;
    state.baseScanId = state.urlState.baseScanId;
    state.filters = {
      q: state.urlState.q || "",
      type: state.urlState.type || "",
      minSize: state.urlState.minSize || 0,
      sort: state.urlState.sort || "",
    };

    renderRootPath();
    await loadHistory();
    ensureValidSortForMode();
    syncSortOptions();
    syncFilterInputs();

    const desiredScanId = state.urlState.scanId || cfg.latest_scan?.id || state.historyItems[0]?.id || null;
    if (!desiredScanId) {
      setStatusChip("idle", "Idle");
      renderAll();
      return;
    }

    await openScan(desiredScanId, { preservePath: true });
  }

  function bindEvents() {
    els.scanButton.addEventListener("click", runScan);
    els.emptyScanButton.addEventListener("click", runScan);
    els.alertRetryButton.addEventListener("click", runScan);
    els.refreshHistoryButton.addEventListener("click", () => {
      loadHistory().catch((err) => showAlert(`Could not refresh scan history: ${err.message}`));
    });
    els.compareBaseSelect.addEventListener("change", () => {
      const value = Number(els.compareBaseSelect.value || 0);
      state.baseScanId = value > 0 ? value : null;
      ensureValidSortForMode();
      syncSortOptions();
      syncFilterInputs();
      reloadCurrentPath().catch((err) => showAlert(`Could not load compare view: ${err.message}`));
    });
    els.clearCompareButton.addEventListener("click", () => {
      state.baseScanId = null;
      ensureValidSortForMode();
      syncSortOptions();
      syncFilterInputs();
      reloadCurrentPath().catch((err) => showAlert(`Could not clear compare mode: ${err.message}`));
    });
    els.applyFiltersButton.addEventListener("click", applyFilters);
    els.resetFiltersButton.addEventListener("click", () => {
      state.filters = { ...defaultFilters };
      ensureValidSortForMode();
      syncSortOptions();
      syncFilterInputs();
      reloadCurrentPath().catch((err) => showAlert(`Could not reset filters: ${err.message}`));
    });
    els.searchInput.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        applyFilters();
      }
    });
    window.addEventListener("resize", scheduleTreemapRender);
  }

  function renderAll() {
    renderRootPath();
    renderBanner();
    renderSummary();
    renderAlert();
    renderToolbarState();
    renderCompareState();
    renderBreadcrumb();
    renderHistory();
    renderDetailList();
    renderChartArea();
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
    const scan = state.latestScan;
    if (!scan || !isScanActive(scan)) {
      els.scanBanner.hidden = true;
      els.scanBannerSummary.textContent = "";
      els.scanBannerDetails.textContent = "";
      delete els.scanBanner.dataset.state;
      return;
    }

    const progress = scan.progress || null;
    const currentPath = progress?.current_path || state.config?.analyze_root || "-";
    const scannedBytes = formatBytes(progress?.scanned_bytes || 0);
    const elapsed = scan.started_at ? formatElapsed(scan.started_at) : "just started";
    const scannedNodes = progress?.scanned_nodes ?? 0;

    els.scanBanner.hidden = false;
    els.scanBanner.dataset.state = scan.status;

    if (scan.status === "queued") {
      els.scanBannerSummary.textContent = `Scan #${scan.id ?? "-"} is queued`;
      els.scanBannerDetails.textContent = `Waiting for progress updates from ${shortPath(currentPath)}`;
      return;
    }

    els.scanBannerSummary.textContent = `Scanning ${scannedNodes} items and ${scannedBytes}`;
    els.scanBannerDetails.textContent = `Current: ${shortPath(currentPath)} | Elapsed: ${elapsed}`;
  }

  function renderSummary() {
    const scan = state.latestScan;
    const currentPath = state.pathLoading
      ? state.currentPath || state.currentView?.path || state.config?.analyze_root || "Not scanned yet"
      : state.currentView?.path || state.currentPath || state.config?.analyze_root || "Not scanned yet";

    let size = "-";
    if (state.currentView) {
      size = formatBytes(state.currentView.totalBytes);
    } else if (scan?.status === "completed") {
      size = formatBytes(scan.total_bytes || 0);
    } else if (scan?.progress) {
      size = formatBytes(scan.progress.scanned_bytes || 0);
    }

    let itemText = "-";
    if (state.currentView) {
      itemText = `${state.currentView.itemCount} ${state.currentView.mode === "compare" ? "changed" : "visible"}`;
    } else if (scan?.progress) {
      itemText = `${scan.progress.scanned_nodes} scanned`;
    } else if (scan?.status === "completed") {
      itemText = `${scan.total_nodes} total`;
    }

    els.summaryPath.textContent = currentPath;
    els.summaryPath.title = currentPath;
    els.summarySize.textContent = size;
    els.summaryItems.textContent = itemText;
    els.summaryScan.textContent = buildScanSummaryText(scan);
  }

  function renderAlert() {
    const alert = state.alert;
    if (!alert) {
      els.appAlert.hidden = true;
      els.appAlertText.textContent = "";
      els.alertRetryButton.hidden = true;
      return;
    }

    els.appAlert.hidden = false;
    els.appAlertText.textContent = alert.message;
    els.alertRetryButton.hidden = !alert.retry;
    els.alertRetryButton.disabled = isScanActive(state.latestScan);
  }

  function renderToolbarState() {
    const compareCandidates = state.historyItems.filter((item) => item.status === "completed" && item.id !== state.activeScanId);
    els.compareBaseSelect.disabled = compareCandidates.length === 0;
    els.clearCompareButton.disabled = !state.baseScanId;
  }

  function renderCompareState() {
    const view = state.currentView;
    if (!view || view.mode !== "compare") {
      els.compareMeta.hidden = true;
      els.compareMeta.textContent = "";
      els.compareSummary.hidden = true;
      els.comparePathState.textContent = "";
      return;
    }

    els.compareMeta.hidden = false;
    els.compareMeta.textContent = view.compareMeta;

    if (!view.compareSummary) {
      els.compareSummary.hidden = true;
      els.comparePathState.textContent = "";
      return;
    }

    const summary = view.compareSummary;
    const deltaClass = summary.delta_bytes >= 0 ? "delta-positive" : "delta-negative";
    els.compareSummary.hidden = false;
    els.comparePathState.innerHTML = [
      `<strong>${escapeHtml(summary.change_class)}</strong>`,
      ` at ${escapeHtml(view.path)}`,
      ` | before ${formatOptionalBytes(summary.before_exists, summary.before_bytes)}`,
      ` | after ${formatOptionalBytes(summary.after_exists, summary.after_bytes)}`,
      ` | <span class="${deltaClass}">${escapeHtml(formatSignedBytes(summary.delta_bytes))}</span>`,
      ` (${escapeHtml(formatPercentSigned(summary.delta_percent))})`,
    ].join("");
  }

  function renderBreadcrumb() {
    els.breadcrumb.innerHTML = "";
    if (!state.config?.analyze_root) {
      return;
    }

    const path = state.pathLoading
      ? state.currentPath || state.currentView?.path || state.config.analyze_root
      : state.currentView?.path || state.currentPath || state.config.analyze_root;
    const parts = buildBreadcrumb(state.config.analyze_root, path);

    parts.forEach((part, index) => {
      const isCurrent = index === parts.length - 1;
      const el = document.createElement(isCurrent ? "span" : "button");
      el.textContent = part.label;
      el.title = part.path;
      el.className = isCurrent ? "breadcrumb-current" : "breadcrumb-button";

      if (!isCurrent) {
        el.type = "button";
        el.addEventListener("click", () => {
          navigateToPath(part.path, part.label);
        });
      }

      els.breadcrumb.appendChild(el);
    });
  }

  function renderHistory() {
    els.historyList.innerHTML = "";

    if (!state.historyItems.length) {
      els.historySummary.textContent = "No scans loaded yet.";
      els.historyEmpty.hidden = false;
      els.historyEmpty.textContent = "Run a scan to build history.";
      return;
    }

    els.historySummary.textContent = `${state.historyItems.length} scans available`;
    els.historyEmpty.hidden = true;

    state.historyItems.forEach((scan) => {
      const row = document.createElement("div");
      row.className = `history-row${scan.id === state.activeScanId ? " active" : ""}`;

      const top = document.createElement("div");
      top.className = "history-top";

      const main = document.createElement("div");
      main.className = "history-main";

      const id = document.createElement("div");
      id.className = "history-id";
      id.textContent = `#${scan.id}`;

      const meta = document.createElement("div");
      meta.className = "history-meta";
      meta.textContent = buildHistoryMeta(scan);

      const status = document.createElement("span");
      status.className = `history-status ${scan.status}`;
      status.textContent = scan.status;

      const size = document.createElement("div");
      size.className = "history-size";
      size.textContent = formatBytes(scan.total_bytes || 0);

      main.append(id, meta);
      top.append(main, status, size);

      const actions = document.createElement("div");
      actions.className = "history-actions";

      const openButton = document.createElement("button");
      openButton.type = "button";
      openButton.className = "history-button";
      openButton.textContent = "Open";
      openButton.addEventListener("click", () => {
        openScan(scan.id, { preservePath: false }).catch((err) => showAlert(`Could not open scan #${scan.id}: ${err.message}`));
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

  function renderDetailList() {
    const view = state.currentView;
    els.detailList.innerHTML = "";

    if (state.pathLoading) {
      els.detailTitle.textContent = isCompareMode() ? "Changed items" : "Largest items";
      els.detailSummary.textContent = "Loading items...";
      els.detailEmpty.hidden = false;
      els.detailEmpty.textContent = "Fetching items for this folder.";
      els.inspectorMeta.textContent = `Loading items from ${basename(state.currentPath) || state.currentPath}`;
      return;
    }

    if (!view) {
      els.detailTitle.textContent = isCompareMode() ? "Changed items" : "Largest items";
      els.detailSummary.textContent = isScanActive(state.latestScan) ? "Scan in progress." : "No scan data yet.";
      els.detailEmpty.hidden = false;
      els.detailEmpty.textContent = isScanActive(state.latestScan)
        ? "The ranked item list will appear after the scan completes."
        : "Run a scan to inspect folders and files.";
      els.inspectorMeta.textContent = isScanActive(state.latestScan)
        ? "A new scan is running. Results will appear here when the root view is ready."
        : "Largest items in the current view.";
      return;
    }

    els.detailTitle.textContent = view.detailTitle;
    els.detailSummary.textContent = view.detailSummary;
    els.inspectorMeta.textContent = view.inspectorMeta;

    if (!view.detailItems.length) {
      els.detailEmpty.hidden = false;
      els.detailEmpty.textContent = view.emptyDetailMessage;
      return;
    }

    els.detailEmpty.hidden = true;
    if (view.mode === "compare") {
      renderCompareDetailItems(view.detailItems);
      return;
    }

    renderLargestDetailItems(view.detailItems);
  }

  function renderLargestDetailItems(items) {
    items.forEach((item) => {
      const row = document.createElement("div");
      row.className = "item-row";

      const main = document.createElement("div");
      main.className = "item-main";

      const nameEl = document.createElement(item.type === "dir" ? "button" : "span");
      nameEl.className = item.type === "dir" ? "item-link" : "item-name";
      nameEl.textContent = item.name;
      nameEl.title = item.path;

      if (item.type === "dir") {
        nameEl.type = "button";
        nameEl.addEventListener("click", () => {
          navigateToPath(item.path, item.name);
        });
      }

      const subtext = document.createElement("div");
      subtext.className = "item-subtext";
      subtext.textContent = shortPath(item.path);
      subtext.title = item.path;

      const type = document.createElement("span");
      type.className = "item-type";
      type.dataset.type = item.type;
      type.textContent = item.type === "dir" ? "Folder" : "File";

      const size = document.createElement("span");
      size.className = "item-size";
      size.textContent = formatBytes(item.size_bytes || 0);

      main.append(nameEl, subtext);
      row.append(main, type, size);
      els.detailList.appendChild(row);
    });
  }

  function renderCompareDetailItems(items) {
    items.forEach((item) => {
      const row = document.createElement("div");
      row.className = "item-row";

      const main = document.createElement("div");
      main.className = "item-main";

      const nameEl = document.createElement(item.type === "dir" ? "button" : "span");
      nameEl.className = item.type === "dir" ? "item-link" : "item-name";
      nameEl.textContent = item.name;
      nameEl.title = item.path;

      if (item.type === "dir") {
        nameEl.type = "button";
        nameEl.addEventListener("click", () => {
          navigateToPath(item.path, item.name);
        });
      }

      const subtext = document.createElement("div");
      subtext.className = "item-subtext";
      subtext.textContent = shortPath(item.path);
      subtext.title = item.path;

      const submeta = document.createElement("div");
      submeta.className = "item-submeta";
      submeta.textContent = `Before: ${formatOptionalBytes(item.before_exists, item.before_bytes)} | After: ${formatOptionalBytes(item.after_exists, item.after_bytes)}`;

      const pill = document.createElement("span");
      pill.className = `change-pill ${item.change_class}`;
      pill.textContent = item.change_class;

      const valueStack = document.createElement("div");
      valueStack.className = "item-value-stack";

      const delta = document.createElement("div");
      delta.className = "item-size";
      delta.textContent = formatSignedBytes(item.delta_bytes);

      const percent = document.createElement("div");
      percent.className = "item-secondary-value";
      percent.textContent = formatPercentSigned(item.delta_percent);

      valueStack.append(delta, percent);
      main.append(nameEl, subtext, submeta);
      row.append(main, pill, valueStack);
      els.detailList.appendChild(row);
    });
  }

  function renderChartArea() {
    hideTooltip();

    if (state.pathLoading && !state.currentView) {
      showChartMessage("Loading folder view...");
      return;
    }

    if (!state.currentView) {
      if (isScanActive(state.latestScan)) {
        showEmptyState(
          "Scan in progress",
          "The treemap will appear here as soon as the first completed scan is ready.",
          false,
        );
        return;
      }

      if (state.latestScan?.status === "failed") {
        showEmptyState(
          "Scan failed",
          "Run another scan to rebuild the treemap for this root path.",
          true,
        );
        return;
      }

      showEmptyState(
        "Run a scan to map this folder",
        "Scan the configured root path to build a treemap, scan history, and compare view.",
        true,
      );
      return;
    }

    if (!state.currentView.chartItems.length) {
      showChartMessage(state.currentView.emptyChartMessage);
      return;
    }

    els.chartFrame.dataset.view = "chart";
    els.chartEmpty.hidden = true;
    els.chartMessage.hidden = true;
    els.chart.hidden = false;
    renderTreemap(state.currentView);
  }

  function showEmptyState(title, body, showAction) {
    els.chartFrame.dataset.view = "empty";
    els.emptyStateTitle.textContent = title;
    els.emptyStateBody.textContent = body;
    els.chartEmpty.hidden = false;
    els.chartMessage.hidden = true;
    els.chart.hidden = true;
    els.emptyScanButton.hidden = !showAction;
    els.emptyScanButton.disabled = isScanActive(state.latestScan);
    els.chart.innerHTML = "";
  }

  function showChartMessage(message) {
    els.chartFrame.dataset.view = "message";
    els.chartEmpty.hidden = true;
    els.chartMessage.hidden = false;
    els.chartMessage.textContent = message;
    els.chart.hidden = true;
    els.chart.innerHTML = "";
  }

  function renderTreemap(view) {
    els.chart.innerHTML = "";

    const width = Math.max(els.chart.clientWidth, 320);
    const height = Math.max(els.chart.clientHeight, 320);
    const data = {
      name: basename(view.path),
      path: view.path,
      children: view.chartItems.map((item) => ({
        ...item,
        value: Math.max(item.visualValue, 1),
      })),
    };

    const root = d3
      .hierarchy(data)
      .sum((item) => item.value || 0)
      .sort((a, b) => (b.value || 0) - (a.value || 0));

    d3.treemap().size([width, height]).paddingInner(3).round(true)(root);

    const svg = d3
      .select(els.chart)
      .append("svg")
      .attr("width", width)
      .attr("height", height)
      .attr("viewBox", `0 0 ${width} ${height}`);

    const node = svg
      .selectAll("g")
      .data(root.leaves())
      .enter()
      .append("g")
      .attr("class", (d) => `node node--${d.data.colorClass}${d.data.clickable ? " node--interactive" : ""}`)
      .attr("transform", (d) => `translate(${d.x0},${d.y0})`);

    node
      .append("rect")
      .attr("width", (d) => Math.max(0, d.x1 - d.x0))
      .attr("height", (d) => Math.max(0, d.y1 - d.y0))
      .attr("rx", 10)
      .attr("ry", 10)
      .on("click", (_, d) => {
        if (!d.data.clickable) {
          return;
        }
        navigateToPath(d.data.path, d.data.name);
      })
      .on("mousemove", (event, d) => {
        showTooltip(event.clientX, event.clientY, d.data.tooltip);
      })
      .on("mouseleave", hideTooltip);

    node
      .filter((d) => shouldShowLabel(d))
      .append("text")
      .attr("class", "node-label")
      .attr("x", 10)
      .attr("y", 18)
      .text((d) => truncateLabel(d.data.name, d.x1 - d.x0, 12));

    node
      .filter((d) => shouldShowMeta(d))
      .append("text")
      .attr("class", "node-meta")
      .attr("x", 10)
      .attr("y", 34)
      .text((d) => d.data.metaLabel);
  }

  async function runScan() {
    if (isScanActive(state.latestScan)) {
      return;
    }

    clearPolling();
    hideTooltip();
    state.alert = null;
    state.currentView = null;
    state.pathLoading = false;
    state.currentPath = state.config?.analyze_root || state.currentPath;
    state.latestScan = {
      id: state.latestScan?.id ?? null,
      status: "queued",
      warning_count: 0,
      progress: null,
    };
    renderStatusState(state.latestScan);

    try {
      const result = await apiPost("/api/v1/scans");
      state.activeScanId = result.scan_id;
      state.latestScan = {
        id: result.scan_id,
        status: "queued",
        warning_count: 0,
        progress: null,
      };
      await loadHistory();
      renderStatusState(state.latestScan);
      startPolling(result.scan_id);
      syncUrlState();
    } catch (err) {
      state.latestScan = {
        ...state.latestScan,
        status: "failed",
        error: err.message,
      };
      showAlert(`Could not start a new scan: ${err.message}`, true);
      renderStatusState(state.latestScan);
    }
  }

  async function openScan(scanId, { preservePath = false } = {}) {
    let selected = state.historyItems.find((item) => item.id === scanId);
    if (!selected) {
      selected = await apiGet(`/api/v1/scans/${encodeURIComponent(scanId)}`);
      upsertHistoryScan(selected);
    }

    state.activeScanId = scanId;
    if (state.baseScanId === scanId) {
      state.baseScanId = null;
    }
    state.latestScan = selected;
    ensureValidSortForMode();
    syncSortOptions();
    syncFilterInputs();
    renderCompareOptions();
    renderStatusState(selected);

    if (isScanActive(selected)) {
      state.currentView = null;
      renderChartArea();
      startPolling(scanId);
      syncUrlState();
      return;
    }

    clearPolling();
    disableScanButtons(false);

    if (selected.status === "failed") {
      state.currentView = null;
      showAlert(`Scan #${selected.id} failed: ${selected.error || "unknown error"}`, true);
      renderStatusState(selected);
      syncUrlState();
      return;
    }

    if (selected.status === "completed") {
      state.alert = null;
      const targetPath = preservePath && state.currentPath ? state.currentPath : state.config?.analyze_root || state.currentPath;
      await loadPath(targetPath);
      return;
    }

    syncUrlState();
  }

  function startPolling(scanId) {
    disableScanButtons(true);
    clearPolling();

    const poll = async () => {
      try {
        const scan = await apiGet(`/api/v1/scans/${scanId}`);
        state.latestScan = scan;
        state.activeScanId = scan.id;
        upsertHistoryScan(scan);
        renderStatusState(scan);

        if (scan.status === "completed") {
          clearPolling();
          disableScanButtons(false);
          logScanWarnings(scan);
          await loadHistory();
          state.currentPath = state.config?.analyze_root || state.currentPath;
          await loadPath(state.currentPath);
          return;
        }

        if (scan.status === "failed") {
          clearPolling();
          disableScanButtons(false);
          state.currentView = null;
          showAlert(`Scan failed: ${scan.error || "unknown error"}`, true);
          renderStatusState(scan);
          return;
        }

        state.pollingHandle = setTimeout(poll, 900);
      } catch (err) {
        clearPolling();
        disableScanButtons(false);
        showAlert(`Could not refresh scan status: ${err.message}`, true);
        state.latestScan = {
          ...state.latestScan,
          status: "failed",
          error: err.message,
        };
        renderStatusState(state.latestScan);
      }
    };

    state.pollingHandle = setTimeout(poll, 250);
  }

  async function loadHistory() {
    const payload = await apiGet("/api/v1/scans?limit=200");
    state.historyItems = (payload.items || []).slice().sort((a, b) => b.id - a.id);
    renderCompareOptions();
    renderHistory();
    renderToolbarState();
  }

  async function deleteScan(scanId) {
    await apiDelete(`/api/v1/scans/${scanId}`);
    await loadHistory();

    if (scanId !== state.activeScanId) {
      return;
    }

    const nextId = state.historyItems[0]?.id || null;
    if (!nextId) {
      state.activeScanId = null;
      state.latestScan = null;
      state.currentView = null;
      state.currentPath = state.config?.analyze_root || null;
      state.baseScanId = null;
      setStatusChip("idle", "Idle");
      renderAll();
      syncUrlState();
      return;
    }

    await openScan(nextId, { preservePath: false });
  }

  async function reloadCurrentPath() {
    if (!state.activeScanId) {
      syncUrlState();
      renderAll();
      return;
    }

    try {
      await loadPath(state.currentPath || state.config?.analyze_root);
    } catch (err) {
      if (!isCompareMode()) {
        throw err;
      }
      state.currentPath = state.config?.analyze_root || state.currentPath;
      await loadPath(state.currentPath);
    }
  }

  async function loadPath(path) {
    if (!state.activeScanId) {
      return;
    }

    const selected = state.historyItems.find((item) => item.id === state.activeScanId) || state.latestScan;
    if (!selected || selected.status !== "completed") {
      return;
    }

    hideTooltip();
    state.currentPath = path;
    state.pathLoading = true;
    state.alert = null;
    renderAll();

    ensureValidSortForMode();
    syncSortOptions();
    syncFilterInputs();

    try {
      if (isCompareMode()) {
        const diffQuery = new URLSearchParams({
          base_scan_id: String(state.baseScanId),
          path,
          limit: "150",
          q: state.filters.q,
          type: state.filters.type,
          min_size: String(state.filters.minSize),
          sort: state.filters.sort,
        });
        const diff = await apiGet(`/api/v1/scans/${state.activeScanId}/diff?${diffQuery.toString()}`);
        state.currentView = buildCompareView(diff);
        state.currentPath = diff.path;
      } else {
        const query = new URLSearchParams({
          path,
          limit: "150",
          q: state.filters.q,
          type: state.filters.type,
          min_size: String(state.filters.minSize),
          sort: state.filters.sort,
        });
        const [children, largest] = await Promise.all([
          apiGet(`/api/v1/scans/${state.activeScanId}/children?${query.toString()}`),
          apiGet(`/api/v1/scans/${state.activeScanId}/largest?${query.toString()}`),
        ]);
        state.currentView = buildNormalView(children, largest.items || []);
        state.currentPath = children.path;
      }
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
      metaLabel: formatBytes(item.size_bytes || 0),
      tooltip: [
        `<strong>${escapeHtml(item.name)}</strong>`,
        escapeHtml(shortPath(item.path)),
        `${formatBytes(item.size_bytes || 0)} (${formatPercent(item.size_bytes || 0, children.total_bytes || 0)})`,
        escapeHtml(item.type),
      ].join("<br>"),
    }));

    return {
      mode: "normal",
      path: children.path,
      totalBytes: children.total_bytes || 0,
      itemCount: (children.children || []).length,
      chartItems,
      emptyChartMessage: "No child items at this path for current filters.",
      detailTitle: "Largest items",
      detailSummary: `${largestItems.length} entries ranked by size`,
      emptyDetailMessage: "No items match the current filters.",
      detailItems: largestItems,
      inspectorMeta: `${largestItems.length} largest items from ${basename(children.path) || children.path}`,
      compareMeta: "",
      compareSummary: null,
    };
  }

  function buildCompareView(diff) {
    const items = diff.items || [];
    const totalBytes = items.reduce((sum, item) => sum + Math.max(item.visual_size_bytes || 0, 1), 0);
    const chartItems = items.map((item) => ({
      name: item.name,
      path: item.path,
      type: item.type,
      visualValue: Math.max(item.visual_size_bytes || 0, 1),
      colorClass: `compare-${compareColorClass(item.change_class)}`,
      clickable: item.type === "dir",
      metaLabel: formatSignedBytes(item.delta_bytes),
      tooltip: [
        `<strong>${escapeHtml(item.name)}</strong>`,
        escapeHtml(shortPath(item.path)),
        `${escapeHtml(item.type)} | ${escapeHtml(item.change_class)}`,
        `Before: ${formatOptionalBytes(item.before_exists, item.before_bytes)}`,
        `After: ${formatOptionalBytes(item.after_exists, item.after_bytes)}`,
        `Delta: ${escapeHtml(formatSignedBytes(item.delta_bytes))} (${escapeHtml(formatPercentSigned(item.delta_percent))})`,
      ].join("<br>"),
    }));

    return {
      mode: "compare",
      path: diff.path,
      totalBytes,
      itemCount: items.length,
      chartItems,
      emptyChartMessage: "No changed items at this path for current compare filters.",
      detailTitle: "Changed items",
      detailSummary: `${items.length} entries ranked by delta`,
      emptyDetailMessage: "No changed items at this path for current compare filters.",
      detailItems: items,
      inspectorMeta: `${items.length} changed items from ${basename(diff.path) || diff.path}`,
      compareMeta: `Compare Base #${diff.base_scan_id} -> Target #${diff.target_scan_id}`,
      compareSummary: diff.summary || null,
    };
  }

  function renderStatusState(scan) {
    disableScanButtons(isScanActive(scan));
    const { stateName, text } = getStatusChip(scan);
    setStatusChip(stateName, text);
    renderAll();
  }

  function renderCompareOptions() {
    const previousValue = String(state.baseScanId || "");
    els.compareBaseSelect.innerHTML = "";

    const none = document.createElement("option");
    none.value = "";
    none.textContent = "None";
    els.compareBaseSelect.appendChild(none);

    const completed = state.historyItems.filter((item) => item.status === "completed" && item.id !== state.activeScanId);
    completed.forEach((scan) => {
      const option = document.createElement("option");
      option.value = String(scan.id);
      option.textContent = `#${scan.id} (${formatBytes(scan.total_bytes || 0)})`;
      els.compareBaseSelect.appendChild(option);
    });

    if (completed.some((item) => String(item.id) === previousValue)) {
      state.baseScanId = Number(previousValue);
      els.compareBaseSelect.value = previousValue;
    } else {
      state.baseScanId = null;
      els.compareBaseSelect.value = "";
    }
  }

  function syncSortOptions() {
    const options = isCompareMode() ? sortOptions.compare : sortOptions.normal;
    els.sortSelect.innerHTML = "";
    options.forEach((optionDef) => {
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
    els.sortSelect.value = state.filters.sort || defaultSortForMode();
  }

  function applyFilters() {
    state.filters = {
      q: (els.searchInput.value || "").trim(),
      type: (els.typeFilter.value || "").trim(),
      minSize: Math.max(0, Number.parseInt(els.minSizeInput.value || "0", 10) || 0),
      sort: (els.sortSelect.value || "").trim(),
    };
    ensureValidSortForMode();
    syncSortOptions();
    syncFilterInputs();
    reloadCurrentPath().catch((err) => showAlert(`Could not apply filters: ${err.message}`));
  }

  function isCompareMode() {
    return Boolean(state.baseScanId && state.activeScanId && state.baseScanId !== state.activeScanId);
  }

  function ensureValidSortForMode() {
    const allowed = allowedSortsForMode();
    if (!allowed.includes(state.filters.sort)) {
      state.filters.sort = defaultSortForMode();
    }
  }

  function allowedSortsForMode() {
    return (isCompareMode() ? sortOptions.compare : sortOptions.normal).map((item) => item.value);
  }

  function defaultSortForMode() {
    return isCompareMode() ? "delta_desc" : "size_desc";
  }

  function navigateToPath(path, label) {
    loadPath(path).catch((err) => showAlert(`Could not open ${label || basename(path)}: ${err.message}`));
  }

  function scheduleTreemapRender() {
    if (!state.currentView || state.pathLoading) {
      return;
    }

    clearTimeout(state.resizeHandle);
    state.resizeHandle = setTimeout(() => {
      if (state.currentView && !state.pathLoading) {
        renderTreemap(state.currentView);
      }
    }, 120);
  }

  function showAlert(message, retry = false) {
    state.alert = { message, retry };
    renderAlert();
  }

  function upsertHistoryScan(scan) {
    const index = state.historyItems.findIndex((item) => item.id === scan.id);
    if (index >= 0) {
      state.historyItems[index] = scan;
    } else {
      state.historyItems.unshift(scan);
    }
    state.historyItems.sort((a, b) => b.id - a.id);
    renderCompareOptions();
    renderHistory();
  }

  function showTooltip(x, y, html) {
    els.tooltip.hidden = false;
    els.tooltip.innerHTML = html;

    const offset = 14;
    const tooltipRect = els.tooltip.getBoundingClientRect();
    const maxLeft = window.innerWidth - tooltipRect.width - 12;
    const maxTop = window.innerHeight - tooltipRect.height - 12;
    const left = Math.min(x + offset, Math.max(12, maxLeft));
    const top = Math.min(y + offset, Math.max(12, maxTop));

    els.tooltip.style.left = `${left}px`;
    els.tooltip.style.top = `${top}px`;
  }

  function hideTooltip() {
    els.tooltip.hidden = true;
  }

  function disableScanButtons(disabled) {
    els.scanButton.disabled = disabled;
    els.emptyScanButton.disabled = disabled;
    els.alertRetryButton.disabled = disabled;
  }

  function setStatusChip(stateName, text) {
    els.scanState.dataset.state = stateName;
    els.scanState.textContent = text;
  }

  function getStatusChip(scan) {
    if (!scan) {
      return { stateName: "idle", text: "Idle" };
    }
    if (scan.status === "queued") {
      return { stateName: "queued", text: "Queued" };
    }
    if (scan.status === "running") {
      return { stateName: "running", text: "Scanning" };
    }
    if (scan.status === "completed") {
      return { stateName: "completed", text: "Ready" };
    }
    if (scan.status === "failed") {
      return { stateName: "failed", text: "Failed" };
    }
    return { stateName: "idle", text: scan.status };
  }

  function buildScanSummaryText(scan) {
    if (!scan) {
      return "No scan yet";
    }
    if (scan.status === "queued") {
      return "Queued";
    }
    if (scan.status === "running") {
      const elapsed = scan.started_at ? formatElapsed(scan.started_at) : "starting";
      const warnings = Number(scan.warning_count || 0);
      return warnings > 0 ? `Running for ${elapsed}, ${warnings} warnings` : `Running for ${elapsed}`;
    }
    if (scan.status === "completed") {
      const finished = scan.finished_at ? new Date(scan.finished_at).toLocaleString() : "just now";
      const warnings = Number(scan.warning_count || 0);
      return warnings > 0 ? `Finished ${finished}, ${warnings} warnings` : `Finished ${finished}`;
    }
    if (scan.status === "failed") {
      return scan.error ? `Failed: ${scan.error}` : "Failed";
    }
    return scan.status;
  }

  function buildHistoryMeta(scan) {
    const started = scan.started_at ? new Date(scan.started_at).toLocaleString() : "not started";
    const finished = scan.finished_at ? new Date(scan.finished_at).toLocaleString() : "in progress";
    return `Started ${started} | Finished ${finished} | ${formatScanDuration(scan)}`;
  }

  function shouldShowLabel(node) {
    const width = node.x1 - node.x0;
    const height = node.y1 - node.y0;
    return width >= 92 && height >= 42;
  }

  function shouldShowMeta(node) {
    const width = node.x1 - node.x0;
    const height = node.y1 - node.y0;
    return width >= 120 && height >= 58;
  }

  function truncateLabel(label, width, fontSize) {
    const maxChars = Math.max(6, Math.floor((width - 18) / (fontSize * 0.62)));
    if (label.length <= maxChars) {
      return label;
    }
    return `${label.slice(0, maxChars - 1)}...`;
  }

  function buildBreadcrumb(root, current) {
    if (!current || current === root) {
      return [{ label: root, path: root }];
    }

    const sep = root.includes("\\") ? "\\" : "/";
    const rootClean = root.endsWith(sep) && root.length > 1 ? root.slice(0, -1) : root;
    if (!current.startsWith(rootClean)) {
      return [{ label: current, path: current }];
    }

    const rel = current.slice(rootClean.length).replace(/^[/\\]+/, "");
    if (!rel) {
      return [{ label: rootClean, path: rootClean }];
    }

    const parts = rel.split(/[\\/]+/g);
    const breadcrumb = [{ label: rootClean, path: rootClean }];
    let acc = rootClean;
    for (const name of parts) {
      acc = acc.endsWith(sep) ? `${acc}${name}` : `${acc}${sep}${name}`;
      breadcrumb.push({ label: name, path: acc });
    }
    return breadcrumb;
  }

  function readUrlState() {
    const params = new URLSearchParams(window.location.search);
    return {
      scanId: parsePositiveInt(params.get("scan")),
      baseScanId: parsePositiveInt(params.get("base_scan")),
      path: params.get("path") || null,
      q: (params.get("q") || "").trim(),
      type: (params.get("type") || "").trim(),
      minSize: Math.max(0, parsePositiveInt(params.get("min_size")) || 0),
      sort: (params.get("sort") || "").trim(),
    };
  }

  function syncUrlState() {
    const params = new URLSearchParams();
    if (state.activeScanId) {
      params.set("scan", String(state.activeScanId));
    }
    if (state.currentPath) {
      params.set("path", state.currentPath);
    }
    if (state.baseScanId) {
      params.set("base_scan", String(state.baseScanId));
    }
    if (state.filters.q) {
      params.set("q", state.filters.q);
    }
    if (state.filters.type) {
      params.set("type", state.filters.type);
    }
    if (state.filters.minSize > 0) {
      params.set("min_size", String(state.filters.minSize));
    }
    if (state.filters.sort && state.filters.sort !== defaultSortForMode()) {
      params.set("sort", state.filters.sort);
    }

    const query = params.toString();
    const nextUrl = query ? `${window.location.pathname}?${query}` : window.location.pathname;
    window.history.replaceState({}, "", nextUrl);
  }

  function isScanActive(scan) {
    return scan?.status === "running" || scan?.status === "queued";
  }

  function clearPolling() {
    if (!state.pollingHandle) {
      return;
    }
    clearTimeout(state.pollingHandle);
    state.pollingHandle = null;
  }

  function parsePositiveInt(value) {
    const parsed = Number.parseInt(value || "", 10);
    if (!Number.isFinite(parsed) || parsed <= 0) {
      return null;
    }
    return parsed;
  }

  function basename(path) {
    if (!path) {
      return "";
    }
    const cleaned = path.replace(/[\\/]+$/, "");
    const split = cleaned.split(/[\\/]/g);
    return split[split.length - 1] || path;
  }

  function shortPath(path) {
    if (!path) {
      return "-";
    }
    if (path.length <= 68) {
      return path;
    }
    return `${path.slice(0, 18)}...${path.slice(-42)}`;
  }

  function formatElapsed(startedAtIso) {
    const startedAt = new Date(startedAtIso).getTime();
    if (!Number.isFinite(startedAt)) {
      return "-";
    }

    let seconds = Math.max(0, Math.floor((Date.now() - startedAt) / 1000));
    const hours = Math.floor(seconds / 3600);
    seconds -= hours * 3600;
    const minutes = Math.floor(seconds / 60);
    seconds -= minutes * 60;

    if (hours > 0) {
      return `${hours}h ${minutes}m ${seconds}s`;
    }
    if (minutes > 0) {
      return `${minutes}m ${seconds}s`;
    }
    return `${seconds}s`;
  }

  function formatScanDuration(scan) {
    if (!scan.started_at) {
      return "-";
    }
    const started = new Date(scan.started_at).getTime();
    const finished = scan.finished_at ? new Date(scan.finished_at).getTime() : Date.now();
    if (!Number.isFinite(started) || !Number.isFinite(finished) || finished < started) {
      return "-";
    }
    const seconds = Math.floor((finished - started) / 1000);
    if (seconds < 60) {
      return `${seconds}s`;
    }
    const minutes = Math.floor(seconds / 60);
    const rem = seconds % 60;
    return `${minutes}m ${rem}s`;
  }

  function logScanWarnings(scan) {
    const warnings = Number(scan.warning_count || 0);
    if (warnings <= 0) {
      return;
    }
    console.warn(`Scan #${scan.id} completed with ${warnings} warning(s). Check server logs for permission/path details.`);
  }

  function formatBytes(bytes) {
    const value = Number(bytes || 0);
    if (value < 1024) {
      return `${value} B`;
    }
    const units = ["KB", "MB", "GB", "TB", "PB"];
    let size = value / 1024;
    let unit = 0;
    while (size >= 1024 && unit < units.length - 1) {
      size /= 1024;
      unit += 1;
    }
    return `${size.toFixed(size >= 10 || unit === 0 ? 1 : 2)} ${units[unit]}`;
  }

  function formatSignedBytes(bytes) {
    if (bytes === 0) {
      return "0 B";
    }
    const prefix = bytes > 0 ? "+" : "-";
    return `${prefix}${formatBytes(Math.abs(bytes))}`;
  }

  function formatOptionalBytes(exists, bytes) {
    return exists ? formatBytes(bytes || 0) : "-";
  }

  function formatPercent(part, total) {
    if (!total) {
      return "0.00%";
    }
    return `${((part / total) * 100).toFixed(2)}%`;
  }

  function formatPercentSigned(value) {
    const numeric = Number(value || 0);
    const prefix = numeric > 0 ? "+" : "";
    return `${prefix}${numeric.toFixed(2)}%`;
  }

  function compareColorClass(changeClass) {
    switch (changeClass) {
      case "new":
        return "new";
      case "removed":
        return "removed";
      case "shrunk":
        return "shrunk";
      case "grew":
      default:
        return "grew";
    }
  }

  function escapeHtml(value) {
    return String(value)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#39;");
  }

  async function apiGet(url) {
    const response = await fetch(url, { method: "GET" });
    const body = await response.json();
    if (!response.ok) {
      throw new Error(body.error || `GET ${url} failed`);
    }
    return body;
  }

  async function apiPost(url) {
    const response = await fetch(url, { method: "POST" });
    const body = await response.json();
    if (!response.ok) {
      throw new Error(body.error || `POST ${url} failed`);
    }
    return body;
  }

  async function apiDelete(url) {
    const response = await fetch(url, { method: "DELETE" });
    if (response.status === 204) {
      return;
    }
    const body = await response.json();
    if (!response.ok) {
      throw new Error(body.error || `DELETE ${url} failed`);
    }
  }
})();
