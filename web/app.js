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
    chartFrame: document.getElementById("chartFrame"),
    chartEmpty: document.getElementById("chartEmpty"),
    emptyStateTitle: document.getElementById("emptyStateTitle"),
    emptyStateBody: document.getElementById("emptyStateBody"),
    chartMessage: document.getElementById("chartMessage"),
    chart: document.getElementById("chart"),
    breadcrumb: document.getElementById("breadcrumb"),
    inspectorMeta: document.getElementById("inspectorMeta"),
    largestSummary: document.getElementById("largestSummary"),
    largestList: document.getElementById("largestList"),
    listEmpty: document.getElementById("listEmpty"),
    tooltip: document.getElementById("tooltip"),
  };

  const state = {
    config: null,
    activeScanId: null,
    latestScan: null,
    currentPath: null,
    currentChildren: null,
    currentLargest: [],
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
    renderRootPath();
    renderBanner();
    renderSummary();
    renderAlert();
    renderBreadcrumb();
    renderLargest();
    renderChartArea();
  });

  async function init() {
    bindEvents();
    renderRootPath();
    renderBanner();
    renderSummary();
    renderAlert();
    renderBreadcrumb();
    renderLargest();
    renderChartArea();

    const cfg = await apiGet("/api/v1/config");
    state.config = cfg;
    state.currentPath = cfg.analyze_root;
    renderRootPath();

    if (!cfg.latest_scan) {
      setStatusChip("idle", "Idle");
      renderSummary();
      renderChartArea();
      return;
    }

    state.latestScan = cfg.latest_scan;
    state.activeScanId = cfg.latest_scan.id;

    if (isScanActive(cfg.latest_scan)) {
      renderStatusState(cfg.latest_scan);
      startPolling(cfg.latest_scan.id);
      return;
    }

    if (cfg.latest_scan.status === "completed") {
      renderStatusState(cfg.latest_scan);
      logScanWarnings(cfg.latest_scan);
      await loadPath(state.currentPath);
      return;
    }

    if (cfg.latest_scan.status === "failed") {
      renderStatusState(cfg.latest_scan);
      state.alert = {
        message: `Last scan failed: ${cfg.latest_scan.error || "unknown error"}`,
        retry: true,
      };
      renderAlert();
      renderSummary();
      renderChartArea();
      return;
    }

    renderStatusState(cfg.latest_scan);
  }

  function bindEvents() {
    els.scanButton.addEventListener("click", runScan);
    els.emptyScanButton.addEventListener("click", runScan);
    els.alertRetryButton.addEventListener("click", runScan);
    window.addEventListener("resize", scheduleTreemapRender);
  }

  async function runScan() {
    if (isScanActive(state.latestScan)) {
      return;
    }

    clearPolling();
    hideTooltip();
    state.alert = null;
    state.currentChildren = null;
    state.currentLargest = [];
    state.pathLoading = false;
    state.currentPath = state.config?.analyze_root || state.currentPath;
    state.latestScan = {
      id: state.latestScan?.id ?? null,
      status: "queued",
      warning_count: 0,
      progress: null,
    };

    renderStatusState(state.latestScan);
    renderLargest();
    renderChartArea();

    try {
      const result = await apiPost("/api/v1/scans");
      state.activeScanId = result.scan_id;
      state.latestScan = {
        id: result.scan_id,
        status: "queued",
        warning_count: 0,
        progress: null,
      };
      renderStatusState(state.latestScan);
      startPolling(result.scan_id);
    } catch (err) {
      state.latestScan = {
        ...state.latestScan,
        status: "failed",
        error: err.message,
      };
      state.alert = {
        message: `Could not start a new scan: ${err.message}`,
        retry: true,
      };
      renderStatusState(state.latestScan);
    }
  }

  function startPolling(scanId) {
    disableScanButtons(true);
    clearPolling();

    const poll = async () => {
      try {
        const scan = await apiGet(`/api/v1/scans/${scanId}`);
        state.latestScan = scan;
        state.activeScanId = scan.id;
        renderStatusState(scan);

        if (scan.status === "completed") {
          clearPolling();
          disableScanButtons(false);
          logScanWarnings(scan);
          state.currentPath = state.config?.analyze_root || state.currentPath;
          await loadPath(state.currentPath);
          return;
        }

        if (scan.status === "failed") {
          clearPolling();
          disableScanButtons(false);
          state.alert = {
            message: `Scan failed: ${scan.error || "unknown error"}`,
            retry: true,
          };
          renderStatusState(scan);
          return;
        }

        state.pollingHandle = setTimeout(poll, 900);
      } catch (err) {
        clearPolling();
        disableScanButtons(false);
        state.latestScan = {
          ...state.latestScan,
          status: "failed",
          error: err.message,
        };
        state.alert = {
          message: `Could not refresh scan status: ${err.message}`,
          retry: true,
        };
        renderStatusState(state.latestScan);
      }
    };

    state.pollingHandle = setTimeout(poll, 250);
  }

  async function loadPath(path) {
    if (!state.activeScanId) {
      return;
    }

    hideTooltip();
    state.currentPath = path;
    state.pathLoading = true;
    state.alert = null;
    renderAlert();
    renderSummary();
    renderBreadcrumb();
    renderLargest();
    renderChartArea();

    try {
      const [children, largest] = await Promise.all([
        apiGet(`/api/v1/scans/${state.activeScanId}/children?path=${encodeURIComponent(path)}`),
        apiGet(`/api/v1/scans/${state.activeScanId}/largest?path=${encodeURIComponent(path)}&limit=40`),
      ]);

      state.currentChildren = children;
      state.currentLargest = largest.items || [];
      state.currentPath = children.path;
      state.pathLoading = false;
      renderSummary();
      renderBreadcrumb();
      renderLargest();
      renderChartArea();
    } catch (err) {
      state.pathLoading = false;
      state.alert = {
        message: `Could not load this folder: ${err.message}`,
        retry: false,
      };
      renderAlert();
      renderSummary();
      renderBreadcrumb();
      renderLargest();
      renderChartArea();
    }
  }

  function renderStatusState(scan) {
    renderRootPath();
    renderBanner();
    renderSummary();
    renderAlert();
    renderBreadcrumb();
    renderLargest();
    renderChartArea();
    disableScanButtons(isScanActive(scan));
    const { stateName, text } = getStatusChip(scan);
    setStatusChip(stateName, text);
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
      ? state.currentPath || state.currentChildren?.path || state.config?.analyze_root || "Not scanned yet"
      : state.currentChildren?.path || state.currentPath || state.config?.analyze_root || "Not scanned yet";
    const size = state.currentChildren
      ? formatBytes(state.currentChildren.total_bytes)
      : scan?.status === "completed"
        ? formatBytes(scan.total_bytes)
        : scan?.progress
          ? formatBytes(scan.progress.scanned_bytes)
          : "-";

    let itemText = "-";
    if (state.currentChildren) {
      itemText = `${state.currentChildren.children.length} visible`;
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

  function renderBreadcrumb() {
    els.breadcrumb.innerHTML = "";

    if (!state.config?.analyze_root) {
      return;
    }

    const path = state.pathLoading
      ? state.currentPath || state.currentChildren?.path || state.config.analyze_root
      : state.currentChildren?.path || state.currentPath || state.config.analyze_root;
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
          loadPath(part.path).catch((err) => {
            state.alert = {
              message: `Could not open ${part.label}: ${err.message}`,
              retry: false,
            };
            renderAlert();
          });
        });
      }

      els.breadcrumb.appendChild(el);
    });
  }

  function renderLargest() {
    const items = state.currentLargest || [];
    els.largestList.innerHTML = "";

    if (!state.currentChildren && !state.pathLoading) {
      els.inspectorMeta.textContent = isScanActive(state.latestScan)
        ? "A new scan is running. Results will appear here when the root view is ready."
        : "Largest items in the current view.";
    } else if (state.currentChildren) {
      els.inspectorMeta.textContent = `${items.length} largest items from ${basename(state.currentChildren.path) || state.currentChildren.path}`;
    }

    if (state.pathLoading) {
      els.inspectorMeta.textContent = `Loading items from ${basename(state.currentPath) || state.currentPath}`;
      els.largestSummary.textContent = "Loading items...";
      els.listEmpty.hidden = false;
      els.listEmpty.textContent = "Fetching ranked items for this folder.";
      return;
    }

    if (!state.currentChildren) {
      els.largestSummary.textContent = isScanActive(state.latestScan)
        ? "Scan in progress."
        : "No scan data yet.";
      els.listEmpty.hidden = false;
      els.listEmpty.textContent = isScanActive(state.latestScan)
        ? "The ranked item list will appear after the scan completes."
        : "Run a scan to inspect folders and files.";
      return;
    }

    els.largestSummary.textContent = `${items.length} entries ranked by size`;

    if (items.length === 0) {
      els.listEmpty.hidden = false;
      els.listEmpty.textContent = "This folder has no child items.";
      return;
    }

    els.listEmpty.hidden = true;

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
          loadPath(item.path).catch((err) => {
            state.alert = {
              message: `Could not open ${item.name}: ${err.message}`,
              retry: false,
            };
            renderAlert();
          });
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
      size.textContent = formatBytes(item.size_bytes);

      main.append(nameEl, subtext);
      row.append(main, type, size);
      els.largestList.appendChild(row);
    });
  }

  function renderChartArea() {
    hideTooltip();

    if (state.pathLoading && !state.currentChildren) {
      showChartMessage("Loading folder view...");
      return;
    }

    if (!state.currentChildren) {
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
        "Scan the configured root path to build a treemap and ranked item list.",
        true,
      );
      return;
    }

    if ((state.currentChildren.children || []).length === 0) {
      showChartMessage("This folder has no child items to visualize.");
      return;
    }

    els.chartFrame.dataset.view = "chart";
    els.chartEmpty.hidden = true;
    els.chartMessage.hidden = true;
    els.chart.hidden = false;
    renderTreemap(state.currentChildren);
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

  function renderTreemap(childrenResponse) {
    const items = childrenResponse.children || [];
    els.chart.innerHTML = "";

    const width = Math.max(els.chart.clientWidth, 320);
    const height = Math.max(els.chart.clientHeight, 320);

    const data = {
      name: basename(childrenResponse.path),
      path: childrenResponse.path,
      children: items.map((item) => ({
        name: item.name,
        path: item.path,
        type: item.type,
        value: Math.max(item.size_bytes, 0),
      })),
    };

    const root = d3
      .hierarchy(data)
      .sum((d) => d.value || 0)
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
      .attr("class", (d) => `node node--${d.data.type}${d.data.type === "dir" ? " node--interactive" : ""}`)
      .attr("transform", (d) => `translate(${d.x0},${d.y0})`);

    node
      .append("rect")
      .attr("width", (d) => Math.max(0, d.x1 - d.x0))
      .attr("height", (d) => Math.max(0, d.y1 - d.y0))
      .attr("rx", 10)
      .attr("ry", 10)
      .on("click", (_, d) => {
        if (d.data.type !== "dir") {
          return;
        }
        loadPath(d.data.path).catch((err) => {
          state.alert = {
            message: `Could not open ${d.data.name}: ${err.message}`,
            retry: false,
          };
          renderAlert();
        });
      })
      .on("mousemove", (event, d) => {
        const percent = childrenResponse.total_bytes > 0
          ? ((d.value / childrenResponse.total_bytes) * 100).toFixed(1)
          : "0.0";
        const typeLabel = d.data.type === "dir" ? "Folder" : "File";
        showTooltip(event.clientX, event.clientY, [
          `<strong>${escapeHtml(d.data.name)}</strong>`,
          escapeHtml(shortPath(d.data.path)),
          `${formatBytes(d.value)} (${percent}%)`,
          typeLabel,
        ].join("<br>"));
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
      .text((d) => formatBytes(d.value));
  }

  function scheduleTreemapRender() {
    if (!state.currentChildren || state.pathLoading) {
      return;
    }

    clearTimeout(state.resizeHandle);
    state.resizeHandle = setTimeout(() => {
      if (state.currentChildren && !state.pathLoading) {
        renderTreemap(state.currentChildren);
      }
    }, 120);
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
      const finished = scan.finished_at
        ? new Date(scan.finished_at).toLocaleString()
        : "just now";
      const warnings = Number(scan.warning_count || 0);
      return warnings > 0 ? `Finished ${finished}, ${warnings} warnings` : `Finished ${finished}`;
    }

    if (scan.status === "failed") {
      return scan.error ? `Failed: ${scan.error}` : "Failed";
    }

    return scan.status;
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
    return `${label.slice(0, maxChars - 1)}…`;
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
})();
