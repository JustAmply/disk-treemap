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
    compareMeta: document.getElementById("compareMeta"),
    compareSummary: document.getElementById("compareSummary"),
    comparePathState: document.getElementById("comparePathState"),
    detailTitle: document.getElementById("detailTitle"),
    detailHead: document.getElementById("detailHead"),
    detailBody: document.getElementById("detailBody"),
    detailEmpty: document.getElementById("detailEmpty"),
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
    state.baseScanId = state.urlState.baseScanId;
    state.filters = {
      q: state.urlState.q || "",
      type: state.urlState.type || "",
      minSize: state.urlState.minSize || 0,
      sort: state.urlState.sort || "",
    };

    els.rootPath.textContent = `Root: ${cfg.analyze_root}`;

    await loadHistory();
    ensureValidSortForMode();
    syncSortOptions();
    syncFilterInputs();

    const desiredScanId = state.urlState.scanId || cfg.latest_scan?.id || state.historyItems[0]?.id || null;
    if (!desiredScanId) {
      setScanState("No scans yet");
      clearLiveStatus();
      clearRenderedResults("No scan data available.");
      return;
    }

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
      ensureValidSortForMode();
      syncSortOptions();
      syncFilterInputs();
      reloadCurrentPath().catch((err) => setScanState(`Diff error: ${err.message}`));
    });
    els.clearCompareButton.addEventListener("click", () => {
      state.baseScanId = null;
      ensureValidSortForMode();
      syncSortOptions();
      syncFilterInputs();
      reloadCurrentPath().catch((err) => setScanState(`Load error: ${err.message}`));
    });
    els.applyFiltersButton.addEventListener("click", applyFilters);
    els.resetFiltersButton.addEventListener("click", () => {
      state.filters = { ...defaultFilters };
      ensureValidSortForMode();
      syncSortOptions();
      syncFilterInputs();
      reloadCurrentPath().catch((err) => setScanState(`Load error: ${err.message}`));
    });
    els.searchInput.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        applyFilters();
      }
    });
  }

  function isCompareMode() {
    return Boolean(state.baseScanId && state.activeScanId && state.baseScanId !== state.activeScanId);
  }

  function defaultSortForMode() {
    return isCompareMode() ? "delta_desc" : "size_desc";
  }

  function allowedSortsForMode() {
    return (isCompareMode() ? sortOptions.compare : sortOptions.normal).map((item) => item.value);
  }

  function ensureValidSortForMode() {
    const allowed = allowedSortsForMode();
    if (!allowed.includes(state.filters.sort)) {
      state.filters.sort = defaultSortForMode();
    }
  }

  function syncSortOptions() {
    const options = isCompareMode() ? sortOptions.compare : sortOptions.normal;
    els.sortSelect.innerHTML = "";
    for (const optionDef of options) {
      const option = document.createElement("option");
      option.value = optionDef.value;
      option.textContent = optionDef.label;
      els.sortSelect.appendChild(option);
    }
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
    reloadCurrentPath().catch((err) => setScanState(`Load error: ${err.message}`));
  }

  async function reloadCurrentPath() {
    if (!state.activeScanId || !state.currentPath) {
      syncUrlState();
      return;
    }

    try {
      await loadPath(state.currentPath);
    } catch (err) {
      if (!isCompareMode()) {
        throw err;
      }
      state.currentPath = state.config.analyze_root;
      await loadPath(state.currentPath);
    }
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
        if (!state.historyItems.length) {
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
    }

    const poll = async () => {
      try {
        const scan = await apiGet(`/api/v1/scans/${scanId}`);
        upsertHistoryScan(scan);
        updateMeta(scan);
        updateLiveStatus(scan);

        if (scan.status === "completed") {
          clearLiveStatus();
          setScanState(`Completed (warnings: ${scan.warning_count || 0})`);
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
          disableScanButton(false);
          state.pollingHandle = null;
          await loadHistory();
          clearRenderedResults(`Scan #${scan.id} failed. No data to display.`);
          return;
        }

        setScanState(`Scan ${scan.status}`, true);
        state.pollingHandle = setTimeout(poll, 250);
      } catch (err) {
        setScanState(`Polling error: ${err.message}`);
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

    ensureValidSortForMode();
    syncSortOptions();
    syncFilterInputs();

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
      state.currentPath = diff.path;
      renderBreadcrumb(diff.path);
      renderCompareSummary(diff);
      renderCompareTreemap(diff);
      renderCompareItems(diff.items || []);
      syncUrlState();
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

    const [children, largest] = await Promise.all([
      apiGet(`/api/v1/scans/${state.activeScanId}/children?${query.toString()}`),
      apiGet(`/api/v1/scans/${state.activeScanId}/largest?${query.toString()}`),
    ]);

    state.currentPath = children.path;
    renderBreadcrumb(children.path);
    hideCompareSummary();
    renderTreemapNodes(
      children.path,
      children.total_bytes || 0,
      (children.children || []).map((item) => ({
        name: item.name,
        path: item.path,
        kind: item.type,
        value: Math.max(item.size_bytes || 0, 0),
        color: item.type === "dir" ? "#8ecae6" : "#ffb703",
        clickable: item.type === "dir",
        tooltip: [
          `<strong>${escapeHtml(item.name)}</strong>`,
          escapeHtml(item.path),
          `${formatBytes(item.size_bytes || 0)} (${formatPercent(item.size_bytes || 0, children.total_bytes || 0)})`,
          escapeHtml(item.type),
        ].join("<br>"),
      })),
      "No child items at this path for current filters."
    );
    renderLargest(largest.items || []);
    syncUrlState();
  }

  function clearRenderedResults(message = "No scan data available.") {
    state.currentPath = state.config?.analyze_root || null;
    els.chart.innerHTML = "";
    els.chartHint.hidden = false;
    els.chartHint.textContent = message;
    els.breadcrumb.innerHTML = "";
    els.detailBody.innerHTML = "";
    els.detailHead.innerHTML = "";
    els.detailEmpty.hidden = true;
    hideCompareSummary();
    syncUrlState();
  }

  function renderTreemapNodes(path, totalBytes, items, emptyMessage) {
    els.chart.innerHTML = "";

    if (!items.length) {
      els.chartHint.hidden = false;
      els.chartHint.textContent = emptyMessage;
      return;
    }

    els.chartHint.hidden = true;
    const width = Math.max(els.chart.clientWidth, 300);
    const height = Math.max(els.chart.clientHeight, 320);
    const data = {
      name: basename(path),
      path,
      children: items.map((item) => ({
        ...item,
        value: Math.max(item.value, 1),
      })),
    };

    const root = d3
      .hierarchy(data)
      .sum((item) => item.value || 0)
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
      .attr("fill", (d) => d.data.color)
      .attr("stroke", "#ffffff")
      .style("cursor", (d) => (d.data.clickable ? "pointer" : "default"))
      .on("click", (_, d) => {
        if (d.data.clickable) {
          loadPath(d.data.path).catch((err) => setScanState(`Navigation error: ${err.message}`));
        }
      })
      .on("mousemove", (event, d) => {
        showTooltip(event.clientX, event.clientY, d.data.tooltip);
      })
      .on("mouseleave", hideTooltip);

    node
      .append("text")
      .attr("class", "node-label")
      .attr("x", 6)
      .attr("y", 16)
      .text((d) => d.data.name)
      .style("display", (d) => {
        const widthPx = d.x1 - d.x0;
        const heightPx = d.y1 - d.y0;
        return widthPx > 90 && heightPx > 28 ? "block" : "none";
      });
  }

  function renderCompareSummary(diff) {
    const summary = diff.summary || null;
    els.compareMeta.hidden = false;
    els.compareMeta.textContent = `Compare Base #${diff.base_scan_id} -> Target #${diff.target_scan_id}`;

    if (!summary) {
      els.compareSummary.hidden = true;
      els.comparePathState.textContent = "";
      return;
    }

    els.compareSummary.hidden = false;
    const deltaClass = summary.delta_bytes >= 0 ? "delta-positive" : "delta-negative";
    els.comparePathState.innerHTML = [
      `<strong>${escapeHtml(summary.change_class)}</strong>`,
      ` at ${escapeHtml(diff.path)}`,
      ` | before ${formatOptionalBytes(summary.before_exists, summary.before_bytes)}`,
      ` | after ${formatOptionalBytes(summary.after_exists, summary.after_bytes)}`,
      ` | <span class="${deltaClass}">${escapeHtml(formatSignedBytes(summary.delta_bytes))}</span>`,
      ` (${escapeHtml(formatPercentSigned(summary.delta_percent))})`,
    ].join("");
  }

  function hideCompareSummary() {
    els.compareMeta.hidden = true;
    els.compareMeta.textContent = "";
    els.compareSummary.hidden = true;
    els.comparePathState.textContent = "";
  }

  function renderCompareTreemap(diff) {
    const items = diff.items || [];
    const totalVisual = items.reduce((sum, item) => sum + Math.max(item.visual_size_bytes || 0, 1), 0);
    renderTreemapNodes(
      diff.path,
      totalVisual,
      items.map((item) => ({
        name: item.name,
        path: item.path,
        kind: item.type,
        value: Math.max(item.visual_size_bytes || 0, 1),
        color: compareColor(item.change_class),
        clickable: item.type === "dir",
        tooltip: [
          `<strong>${escapeHtml(item.name)}</strong>`,
          escapeHtml(item.path),
          `${escapeHtml(item.type)} | ${escapeHtml(item.change_class)}`,
          `Before: ${formatOptionalBytes(item.before_exists, item.before_bytes)}`,
          `After: ${formatOptionalBytes(item.after_exists, item.after_bytes)}`,
          `Delta: ${escapeHtml(formatSignedBytes(item.delta_bytes))} (${escapeHtml(formatPercentSigned(item.delta_percent))})`,
          `Visual size: ${formatBytes(item.visual_size_bytes || 0)}`,
        ].join("<br>"),
      })),
      "No changed items at this path for current compare filters."
    );
  }

  function renderLargest(items) {
    els.detailTitle.textContent = "Largest Items";
    setDetailColumns(["Name", "Type", "Size"]);
    els.detailBody.innerHTML = "";

    for (const item of items) {
      const tr = document.createElement("tr");
      const nameTd = document.createElement("td");
      nameTd.appendChild(createNameCell(item.name, item.path, item.type === "dir"));

      const typeTd = document.createElement("td");
      typeTd.textContent = item.type;

      const sizeTd = document.createElement("td");
      sizeTd.textContent = formatBytes(item.size_bytes || 0);

      tr.append(nameTd, typeTd, sizeTd);
      els.detailBody.appendChild(tr);
    }

    const hasRows = items.length > 0;
    els.detailEmpty.hidden = hasRows;
    els.detailEmpty.textContent = hasRows ? "" : "No items match the current filters.";
  }

  function renderCompareItems(items) {
    els.detailTitle.textContent = "Changed Items";
    setDetailColumns(["Name", "Type", "Before", "After", "Delta", "Change"]);
    els.detailBody.innerHTML = "";
    for (const item of items) {
      const tr = document.createElement("tr");
      const nameTd = document.createElement("td");
      nameTd.appendChild(createNameCell(item.name, item.path, item.type === "dir", `compare-link ${item.change_class}`));

      const typeTd = document.createElement("td");
      typeTd.textContent = item.type;

      const beforeTd = document.createElement("td");
      beforeTd.textContent = formatOptionalBytes(item.before_exists, item.before_bytes);

      const afterTd = document.createElement("td");
      afterTd.textContent = formatOptionalBytes(item.after_exists, item.after_bytes);

      const deltaTd = document.createElement("td");
      deltaTd.textContent = `${formatSignedBytes(item.delta_bytes)} (${formatPercentSigned(item.delta_percent)})`;

      const changeTd = document.createElement("td");
      const pill = document.createElement("span");
      pill.className = `change-pill ${item.change_class}`;
      pill.textContent = item.change_class;
      changeTd.appendChild(pill);

      tr.append(nameTd, typeTd, beforeTd, afterTd, deltaTd, changeTd);
      els.detailBody.appendChild(tr);
    }

    const hasRows = items.length > 0;
    els.detailEmpty.hidden = hasRows;
    els.detailEmpty.textContent = hasRows ? "" : "No changed items at this path for current compare filters.";
  }

  function createNameCell(name, path, clickable, className = "cell-link") {
    if (!clickable) {
      const span = document.createElement("span");
      span.textContent = name;
      return span;
    }

    const link = document.createElement("a");
    link.href = "#";
    link.className = className;
    link.textContent = name;
    link.addEventListener("click", (event) => {
      event.preventDefault();
      loadPath(path).catch((err) => setScanState(`Navigation error: ${err.message}`));
    });
    return link;
  }

  function setDetailColumns(columns) {
    els.detailHead.innerHTML = "";
    for (const label of columns) {
      const th = document.createElement("th");
      th.textContent = label;
      els.detailHead.appendChild(th);
    }
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
      statusTd.textContent = `${scan.status} (${formatScanDuration(scan)})`;

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
                clearRenderedResults("No scan data available.");
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

    if (completed.some((item) => String(item.id) === previousValue)) {
      state.baseScanId = Number(previousValue);
      els.compareBaseSelect.value = previousValue;
    } else {
      state.baseScanId = null;
      els.compareBaseSelect.value = "";
    }

    ensureValidSortForMode();
    syncSortOptions();
    syncFilterInputs();
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

    els.scanMeta.textContent = `Scan #${scan.id} | status: ${scan.status} | started: ${started} | finished: ${finished} | nodes: ${scan.total_nodes} | total: ${formatBytes(scan.total_bytes || 0)} | warnings: ${scan.warning_count || 0}`;
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
    const newUrl = query ? `${window.location.pathname}?${query}` : window.location.pathname;
    window.history.replaceState({}, "", newUrl);
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

  function parsePositiveInt(value) {
    const parsed = Number.parseInt(value || "", 10);
    if (!Number.isFinite(parsed) || parsed <= 0) {
      return null;
    }
    return parsed;
  }

  function basename(path) {
    const cleaned = path.replace(/[\\/]+$/, "");
    const split = cleaned.split(/[\\/]+/g);
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
    const updatedAt = progress?.updated_at ? new Date(progress.updated_at).toLocaleTimeString() : "awaiting first item";

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

  function compareColor(changeClass) {
    switch (changeClass) {
      case "new":
        return "#93c5fd";
      case "removed":
        return "#fdba74";
      case "shrunk":
        return "#fb923c";
      case "grew":
      default:
        return "#6ee7b7";
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
