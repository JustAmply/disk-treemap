(() => {
  const els = {
    rootPath: document.getElementById("rootPath"),
    scanButton: document.getElementById("scanButton"),
    scanState: document.getElementById("scanState"),
    scanLive: document.getElementById("scanLive"),
    scanLiveSummary: document.getElementById("scanLiveSummary"),
    scanLiveDetails: document.getElementById("scanLiveDetails"),
    chart: document.getElementById("chart"),
    chartHint: document.getElementById("chartHint"),
    largestTable: document.getElementById("largestTable"),
    breadcrumb: document.getElementById("breadcrumb"),
    scanMeta: document.getElementById("scanMeta"),
    tooltip: document.getElementById("tooltip"),
    historyTable: document.getElementById("historyTable"),
    refreshHistoryButton: document.getElementById("refreshHistoryButton"),
    compareBaseSelect: document.getElementById("compareBaseSelect"),
    clearCompareButton: document.getElementById("clearCompareButton"),
    searchInput: document.getElementById("searchInput"),
    typeFilter: document.getElementById("typeFilter"),
    minSizeInput: document.getElementById("minSizeInput"),
    sortSelect: document.getElementById("sortSelect"),
    applyFiltersButton: document.getElementById("applyFiltersButton"),
    resetFiltersButton: document.getElementById("resetFiltersButton"),
    diffPanel: document.getElementById("diffPanel"),
    diffMeta: document.getElementById("diffMeta"),
    diffEmpty: document.getElementById("diffEmpty"),
    growthTable: document.getElementById("growthTable"),
    shrinkTable: document.getElementById("shrinkTable"),
  };

  const defaultFilters = {
    q: "",
    type: "",
    minSize: 0,
    sort: "size_desc",
  };

  const state = {
    config: null,
    activeScanId: null,
    currentPath: null,
    pollingHandle: null,
    historyItems: [],
    baseScanId: null,
    filters: { ...defaultFilters },
    urlState: readUrlState(),
  };

  init().catch((err) => {
    setScanState(`Error: ${err.message}`);
  });

  async function init() {
    bindEvents();

    const cfg = await apiGet("/api/v1/config");
    state.config = cfg;
    state.currentPath = state.urlState.path || cfg.analyze_root;

    state.filters = {
      q: state.urlState.q || "",
      type: state.urlState.type || "",
      minSize: state.urlState.minSize || 0,
      sort: state.urlState.sort || "size_desc",
    };
    syncFilterInputs();

    els.rootPath.textContent = `Root: ${cfg.analyze_root}`;

    await loadHistory();

    const desiredScanId = state.urlState.scanId || cfg.latest_scan?.id || state.historyItems[0]?.id || null;
    if (!desiredScanId) {
      setScanState("No scans yet");
      clearLiveStatus();
      renderDiff(null);
      return;
    }

    state.baseScanId = state.urlState.baseScanId;
    await openScan(desiredScanId, { preservePath: true });
  }

  function bindEvents() {
    els.scanButton.addEventListener("click", runScan);
    els.refreshHistoryButton.addEventListener("click", () => {
      loadHistory().catch((err) => setScanState(`History error: ${err.message}`));
    });
    els.compareBaseSelect.addEventListener("change", () => {
      const value = Number(els.compareBaseSelect.value || 0);
      state.baseScanId = value > 0 ? value : null;
      if (!state.activeScanId || !state.currentPath) {
        syncUrlState();
        renderDiff(null);
        return;
      }
      loadPath(state.currentPath).catch((err) => setScanState(`Diff error: ${err.message}`));
    });
    els.clearCompareButton.addEventListener("click", () => {
      state.baseScanId = null;
      els.compareBaseSelect.value = "";
      syncUrlState();
      renderDiff(null);
    });

    els.applyFiltersButton.addEventListener("click", applyFilters);
    els.resetFiltersButton.addEventListener("click", () => {
      state.filters = { ...defaultFilters };
      syncFilterInputs();
      if (state.activeScanId && state.currentPath) {
        loadPath(state.currentPath).catch((err) => setScanState(`Load error: ${err.message}`));
      }
      syncUrlState();
    });
    els.searchInput.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        applyFilters();
      }
    });
  }

  function applyFilters() {
    state.filters = {
      q: (els.searchInput.value || "").trim(),
      type: (els.typeFilter.value || "").trim(),
      minSize: Math.max(0, Number.parseInt(els.minSizeInput.value || "0", 10) || 0),
      sort: (els.sortSelect.value || "size_desc").trim(),
    };

    if (state.activeScanId && state.currentPath) {
      loadPath(state.currentPath).catch((err) => setScanState(`Load error: ${err.message}`));
    }
    syncUrlState();
  }

  async function loadHistory() {
    const payload = await apiGet("/api/v1/scans?limit=200");
    state.historyItems = payload.items || [];
    renderHistory();
    renderCompareOptions();
  }

  async function openScan(scanId, { preservePath = false } = {}) {
    let selected = state.historyItems.find((item) => item.id === scanId);
    if (!selected) {
      try {
        selected = await apiGet(`/api/v1/scans/${encodeURIComponent(scanId)}`);
        upsertHistoryScan(selected);
      } catch {
        if (!state.historyItems || state.historyItems.length === 0) {
          throw new Error(`scan #${scanId} not found`);
        }
        selected = state.historyItems[0];
        scanId = selected.id;
      }
    }

    state.activeScanId = scanId;
    renderHistory();
    renderCompareOptions();
    syncUrlState();

    updateMeta(selected);

    if (selected.status === "running" || selected.status === "queued") {
      setScanState(`Scan ${selected.status}`, true);
      updateLiveStatus(selected);
      startPolling(scanId);
      return;
    }

    if (state.pollingHandle) {
      clearTimeout(state.pollingHandle);
      state.pollingHandle = null;
    }

    clearLiveStatus();
    disableScanButton(false);

    if (selected.status === "failed") {
      setScanState(`Failed: ${selected.error || "unknown error"}`);
      clearRenderedResults(`Scan #${selected.id} failed. No data to display.`);
      return;
    }

    if (selected.status === "completed") {
      setScanState(`Completed (warnings: ${selected.warning_count || 0})`);
      const targetPath = preservePath && state.currentPath ? state.currentPath : state.config.analyze_root;
      await loadPath(targetPath);
      return;
    }

    setScanState(`Scan ${selected.status}`);
  }

  async function runScan() {
    disableScanButton(true);
    setScanState("Starting scan...", true);
    showPendingLiveStatus();

    try {
      const result = await apiPost("/api/v1/scans");
      state.activeScanId = result.scan_id;
      state.currentPath = state.config.analyze_root;
      setScanState(`Scan #${state.activeScanId} running`, true);
      await loadHistory();
      startPolling(state.activeScanId);
      syncUrlState();
    } catch (err) {
      clearLiveStatus();
      disableScanButton(false);
      setScanState(`Failed: ${err.message}`);
    }
  }

  function startPolling(scanId) {
    disableScanButton(true);
    if (state.pollingHandle) {
      clearTimeout(state.pollingHandle);
      state.pollingHandle = null;
    }

    const poll = async () => {
      try {
        const scan = await apiGet(`/api/v1/scans/${scanId}`);
        upsertHistoryScan(scan);
        updateMeta(scan);
        updateLiveStatus(scan);

        if (scan.status === "completed") {
          clearLiveStatus();
          setScanState(`Completed (warnings: ${scan.warning_count})`);
          disableScanButton(false);
          state.pollingHandle = null;
          await loadHistory();
          state.currentPath = state.config.analyze_root;
          await loadPath(state.currentPath);
          return;
        }

        if (scan.status === "failed") {
          clearLiveStatus();
          setScanState(`Failed: ${scan.error || "unknown error"}`);
          clearRenderedResults(`Scan #${scan.id} failed. No data to display.`);
          disableScanButton(false);
          state.pollingHandle = null;
          await loadHistory();
          return;
        }

        if (scan.progress) {
          setScanState(`Scanning ${scan.progress.scanned_nodes} items (${formatBytes(scan.progress.scanned_bytes)})`, true);
        } else {
          setScanState(`Scan ${scan.status}`, true);
        }
        state.pollingHandle = setTimeout(poll, 900);
      } catch (err) {
        clearLiveStatus();
        setScanState(`Status error: ${err.message}`);
        disableScanButton(false);
        state.pollingHandle = null;
      }
    };

    state.pollingHandle = setTimeout(poll, 250);
  }

  function upsertHistoryScan(scan) {
    const index = state.historyItems.findIndex((item) => item.id === scan.id);
    if (index >= 0) {
      state.historyItems[index] = scan;
    } else {
      state.historyItems.unshift(scan);
    }
    renderHistory();
    renderCompareOptions();
  }

  async function loadPath(path) {
    if (!state.activeScanId) {
      return;
    }

    const selected = state.historyItems.find((item) => item.id === state.activeScanId);
    if (!selected || selected.status !== "completed") {
      return;
    }

    const query = new URLSearchParams({
      path,
      limit: "150",
      q: state.filters.q,
      type: state.filters.type,
      min_size: String(state.filters.minSize),
      sort: state.filters.sort,
    });

    const childrenPromise = apiGet(`/api/v1/scans/${state.activeScanId}/children?${query.toString()}`);
    const largestPromise = apiGet(`/api/v1/scans/${state.activeScanId}/largest?${query.toString()}`);

    let diffPromise = Promise.resolve(null);
    if (state.baseScanId && state.baseScanId !== state.activeScanId) {
      const diffQuery = new URLSearchParams({
        base_scan_id: String(state.baseScanId),
        path,
        limit: "100",
      });
      diffPromise = apiGet(`/api/v1/scans/${state.activeScanId}/diff?${diffQuery.toString()}`);
    }

    const [children, largest, diff] = await Promise.all([childrenPromise, largestPromise, diffPromise]);

    state.currentPath = children.path;
    renderBreadcrumb(children.path);
    renderTreemap(children);
    renderLargest(largest.items);
    renderDiff(diff);
    syncUrlState();
  }

  function clearRenderedResults(message = "No scan data available.") {
    state.currentPath = state.config?.analyze_root || null;
    els.chart.innerHTML = "";
    els.chartHint.hidden = false;
    els.chartHint.textContent = message;
    els.breadcrumb.innerHTML = "";
    els.largestTable.innerHTML = "";
    els.scanMeta.textContent = message;
    renderDiff(null);
    syncUrlState();
  }
  function renderTreemap(childrenResponse) {
    els.chart.innerHTML = "";

    const items = childrenResponse.children || [];
    if (items.length === 0) {
      els.chartHint.hidden = false;
      els.chartHint.textContent = "No child items at this path for current filters.";
      return;
    }

    els.chartHint.hidden = true;

    const width = Math.max(els.chart.clientWidth, 300);
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

    d3.treemap().size([width, height]).paddingInner(2)(root);

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
      .attr("transform", (d) => `translate(${d.x0},${d.y0})`);

    node
      .append("rect")
      .attr("width", (d) => Math.max(0, d.x1 - d.x0))
      .attr("height", (d) => Math.max(0, d.y1 - d.y0))
      .attr("fill", (d) => (d.data.type === "dir" ? "#8ecae6" : "#ffb703"))
      .attr("stroke", "#ffffff")
      .style("cursor", (d) => (d.data.type === "dir" ? "pointer" : "default"))
      .on("click", (_, d) => {
        if (d.data.type === "dir") {
          loadPath(d.data.path).catch((err) => setScanState(`Navigation error: ${err.message}`));
        }
      })
      .on("mousemove", (event, d) => {
        const percent =
          childrenResponse.total_bytes > 0
            ? ((d.value / childrenResponse.total_bytes) * 100).toFixed(2)
            : "0.00";
        showTooltip(event.clientX, event.clientY, [
          `<strong>${escapeHtml(d.data.name)}</strong>`,
          d.data.path,
          `${formatBytes(d.value)} (${percent}%)`,
          d.data.type,
        ].join("<br>"));
      })
      .on("mouseleave", hideTooltip);

    node
      .append("text")
      .attr("class", "node-label")
      .attr("x", 6)
      .attr("y", 16)
      .text((d) => d.data.name)
      .style("display", (d) => {
        const w = d.x1 - d.x0;
        const h = d.y1 - d.y0;
        return w > 90 && h > 28 ? "block" : "none";
      });
  }

  function renderLargest(items) {
    els.largestTable.innerHTML = "";

    for (const item of items || []) {
      const tr = document.createElement("tr");

      const nameTd = document.createElement("td");
      const link = document.createElement("a");
      link.textContent = item.name;
      link.href = "#";
      link.className = "cell-link";
      link.addEventListener("click", (event) => {
        event.preventDefault();
        if (item.type === "dir") {
          loadPath(item.path).catch((err) => setScanState(`Navigation error: ${err.message}`));
        }
      });
      nameTd.appendChild(link);

      const typeTd = document.createElement("td");
      typeTd.textContent = item.type;

      const sizeTd = document.createElement("td");
      sizeTd.textContent = formatBytes(item.size_bytes);

      tr.append(nameTd, typeTd, sizeTd);
      els.largestTable.appendChild(tr);
    }
  }

  function renderDiff(diff) {
    els.growthTable.innerHTML = "";
    els.shrinkTable.innerHTML = "";

    if (!diff || !state.baseScanId || state.baseScanId === state.activeScanId) {
      els.diffPanel.hidden = true;
      return;
    }

    const items = diff.items || [];
    const growth = items
      .filter((item) => item.delta_bytes > 0)
      .sort((a, b) => b.delta_bytes - a.delta_bytes)
      .slice(0, 10);
    const shrink = items
      .filter((item) => item.delta_bytes < 0)
      .sort((a, b) => a.delta_bytes - b.delta_bytes)
      .slice(0, 10);

    els.diffPanel.hidden = false;
    els.diffMeta.textContent = `Base #${diff.base_scan_id} -> Target #${diff.target_scan_id} at ${diff.path}`;

    const renderRow = (table, item, cssClass = "") => {
      const tr = document.createElement("tr");
      const nameTd = document.createElement("td");
      const nameLink = document.createElement("a");
      nameLink.href = "#";
      nameLink.className = `cell-link ${cssClass}`.trim();
      nameLink.textContent = item.name;
      nameLink.addEventListener("click", (event) => {
        event.preventDefault();
        loadPath(item.path).catch((err) => setScanState(`Navigation error: ${err.message}`));
      });
      nameTd.appendChild(nameLink);

      const deltaTd = document.createElement("td");
      deltaTd.textContent = formatSignedBytes(item.delta_bytes);

      const pctTd = document.createElement("td");
      pctTd.textContent = `${item.delta_percent.toFixed(2)}%`;

      tr.append(nameTd, deltaTd, pctTd);
      table.appendChild(tr);
    };

    for (const item of growth) {
      renderRow(els.growthTable, item);
    }
    for (const item of shrink) {
      renderRow(els.shrinkTable, item, "warn");
    }

    const hasRows = growth.length > 0 || shrink.length > 0;
    els.diffEmpty.hidden = hasRows;
  }

  function renderBreadcrumb(currentPath) {
    els.breadcrumb.innerHTML = "";

    const parts = buildBreadcrumb(state.config.analyze_root, currentPath);
    for (const part of parts) {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = part.label;
      button.addEventListener("click", () => {
        loadPath(part.path).catch((err) => setScanState(`Navigation error: ${err.message}`));
      });
      els.breadcrumb.appendChild(button);
    }
  }

  function renderHistory() {
    els.historyTable.innerHTML = "";

    for (const scan of state.historyItems) {
      const tr = document.createElement("tr");
      if (scan.id === state.activeScanId) {
        tr.style.background = "#f1f7f6";
      }

      const idTd = document.createElement("td");
      idTd.textContent = `#${scan.id}`;

      const statusTd = document.createElement("td");
      const duration = formatScanDuration(scan);
      statusTd.textContent = `${scan.status} (${duration})`;

      const totalTd = document.createElement("td");
      totalTd.textContent = formatBytes(scan.total_bytes || 0);

      const actionsTd = document.createElement("td");
      const openButton = document.createElement("button");
      openButton.type = "button";
      openButton.textContent = "Open";
      openButton.className = "secondary";
      openButton.addEventListener("click", () => {
        openScan(scan.id).catch((err) => setScanState(`Open scan error: ${err.message}`));
      });

      actionsTd.appendChild(openButton);

      if (scan.status !== "running" && scan.status !== "queued") {
        const deleteButton = document.createElement("button");
        deleteButton.type = "button";
        deleteButton.textContent = "Delete";
        deleteButton.className = "secondary";
        deleteButton.style.marginLeft = "0.35rem";
        deleteButton.addEventListener("click", async () => {
          try {
            await apiDelete(`/api/v1/scans/${scan.id}`);
            await loadHistory();

            if (scan.id === state.activeScanId) {
              const nextId = state.historyItems[0]?.id || null;
              if (nextId) {
                await openScan(nextId, { preservePath: false });
              } else {
                state.activeScanId = null;
                state.currentPath = state.config.analyze_root;
                els.chart.innerHTML = "";
                els.largestTable.innerHTML = "";
                renderDiff(null);
                setScanState("No scans yet");
              }
            }
          } catch (err) {
            setScanState(`Delete failed: ${err.message}`);
          }
        });
        actionsTd.appendChild(deleteButton);
      }

      tr.append(idTd, statusTd, totalTd, actionsTd);
      els.historyTable.appendChild(tr);
    }
  }

  function renderCompareOptions() {
    const previousValue = String(state.baseScanId || "");
    els.compareBaseSelect.innerHTML = "";

    const none = document.createElement("option");
    none.value = "";
    none.textContent = "None";
    els.compareBaseSelect.appendChild(none);

    const completed = state.historyItems.filter((item) => item.status === "completed" && item.id !== state.activeScanId);
    for (const scan of completed) {
      const option = document.createElement("option");
      option.value = String(scan.id);
      option.textContent = `#${scan.id} (${formatBytes(scan.total_bytes || 0)})`;
      els.compareBaseSelect.appendChild(option);
    }

    const hasSelected = completed.some((item) => String(item.id) === previousValue);
    if (hasSelected) {
      state.baseScanId = Number(previousValue);
      els.compareBaseSelect.value = previousValue;
    } else {
      state.baseScanId = null;
      els.compareBaseSelect.value = "";
    }
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

  function updateMeta(scan) {
    const started = scan.started_at ? new Date(scan.started_at).toLocaleString() : "-";
    const finished = scan.finished_at ? new Date(scan.finished_at).toLocaleString() : "-";

    if ((scan.status === "running" || scan.status === "queued") && scan.progress) {
      const progress = scan.progress;
      const updatedAt = progress.updated_at ? new Date(progress.updated_at).toLocaleTimeString() : "-";
      const currentPath = progress.current_path || state.config?.analyze_root || "-";
      els.scanMeta.textContent = `Scan #${scan.id} | status: ${scan.status} | scanned: ${progress.scanned_nodes} items (${progress.scanned_files} files, ${progress.scanned_dirs} dirs) | discovered: ${formatBytes(progress.scanned_bytes)} | current: ${shortPath(currentPath)} | updated: ${updatedAt} | started: ${started}`;
      return;
    }

    els.scanMeta.textContent = `Scan #${scan.id} | status: ${scan.status} | started: ${started} | finished: ${finished} | nodes: ${scan.total_nodes} | total: ${formatBytes(scan.total_bytes)} | warnings: ${scan.warning_count}`;
  }

  function showTooltip(x, y, html) {
    els.tooltip.hidden = false;
    els.tooltip.innerHTML = html;
    els.tooltip.style.left = `${x + 12}px`;
    els.tooltip.style.top = `${y + 12}px`;
  }

  function hideTooltip() {
    els.tooltip.hidden = true;
  }

  function disableScanButton(disabled) {
    els.scanButton.disabled = disabled;
  }

  function setScanState(text, running = false) {
    els.scanState.textContent = text;
    els.scanState.classList.toggle("running", running);
  }

  function syncFilterInputs() {
    els.searchInput.value = state.filters.q;
    els.typeFilter.value = state.filters.type;
    els.minSizeInput.value = String(state.filters.minSize || 0);
    els.sortSelect.value = state.filters.sort || "size_desc";
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
    if (state.filters.sort && state.filters.sort !== "size_desc") {
      params.set("sort", state.filters.sort);
    }

    const query = params.toString();
    const newUrl = query ? `${window.location.pathname}?${query}` : window.location.pathname;
    window.history.replaceState({}, "", newUrl);
  }

  function readUrlState() {
    const params = new URLSearchParams(window.location.search);
    const scanId = parsePositiveInt(params.get("scan"));
    const baseScanId = parsePositiveInt(params.get("base_scan"));
    const minSize = Math.max(0, parsePositiveInt(params.get("min_size")) || 0);
    const sort = params.get("sort") || "size_desc";

    return {
      scanId,
      baseScanId,
      path: params.get("path") || null,
      q: (params.get("q") || "").trim(),
      type: (params.get("type") || "").trim(),
      minSize,
      sort,
    };
  }

  function parsePositiveInt(value) {
    const parsed = Number.parseInt(value || "", 10);
    if (!Number.isFinite(parsed) || parsed <= 0) {
      return null;
    }
    return parsed;
  }

  function basename(path) {
    const cleaned = path.replace(/[\\/]+$/, "");
    const split = cleaned.split(/[\\/]/g);
    return split[split.length - 1] || path;
  }

  function shortPath(path) {
    if (!path) {
      return "-";
    }
    if (path.length <= 72) {
      return path;
    }
    return `...${path.slice(-69)}`;
  }

  function showPendingLiveStatus() {
    els.scanLive.hidden = false;
    els.scanLive.dataset.state = "queued";
    els.scanLiveSummary.textContent = "Status: queued - waiting for first progress update";
    els.scanLiveDetails.textContent = `Root: ${shortPath(state.config?.analyze_root || "-")}`;
  }

  function updateLiveStatus(scan) {
    const isActive = scan.status === "running" || scan.status === "queued";
    if (!isActive) {
      clearLiveStatus();
      return;
    }

    const progress = scan.progress || null;
    const elapsed = scan.started_at ? formatElapsed(scan.started_at) : "-";
    const scannedNodes = progress?.scanned_nodes ?? 0;
    const scannedFiles = progress?.scanned_files ?? 0;
    const scannedDirs = progress?.scanned_dirs ?? 0;
    const scannedBytes = formatBytes(progress?.scanned_bytes ?? 0);
    const currentPath = progress?.current_path || state.config?.analyze_root || "-";
    const updatedAt = progress?.updated_at
      ? new Date(progress.updated_at).toLocaleTimeString()
      : "awaiting first item";

    els.scanLive.hidden = false;
    els.scanLive.dataset.state = scan.status;
    els.scanLiveSummary.textContent = `Status: ${scan.status} - elapsed: ${elapsed} - ${scannedNodes} items (${scannedFiles} files, ${scannedDirs} dirs) - ${scannedBytes}`;
    els.scanLiveDetails.textContent = `Current: ${shortPath(currentPath)} - Updated: ${updatedAt}`;
  }

  function clearLiveStatus() {
    els.scanLive.hidden = true;
    delete els.scanLive.dataset.state;
    els.scanLiveSummary.textContent = "";
    els.scanLiveDetails.textContent = "";
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
    return `${size.toFixed(2)} ${units[unit]}`;
  }

  function formatSignedBytes(bytes) {
    if (bytes === 0) {
      return "0 B";
    }
    const prefix = bytes > 0 ? "+" : "-";
    return `${prefix}${formatBytes(Math.abs(bytes))}`;
  }

  function escapeHtml(value) {
    return value
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

