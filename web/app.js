(() => {
  const els = {
    rootPath: document.getElementById("rootPath"),
    scanButton: document.getElementById("scanButton"),
    scanState: document.getElementById("scanState"),
    chart: document.getElementById("chart"),
    chartHint: document.getElementById("chartHint"),
    largestTable: document.getElementById("largestTable"),
    breadcrumb: document.getElementById("breadcrumb"),
    scanMeta: document.getElementById("scanMeta"),
    tooltip: document.getElementById("tooltip"),
  };

  const state = {
    config: null,
    activeScanId: null,
    currentPath: null,
    pollingHandle: null,
  };

  init().catch((err) => {
    setScanState(`Error: ${err.message}`);
  });

  async function init() {
    els.scanButton.addEventListener("click", runScan);

    const cfg = await apiGet("/api/v1/config");
    state.config = cfg;
    state.currentPath = cfg.analyze_root;

    els.rootPath.textContent = `Root: ${cfg.analyze_root}`;

    if (cfg.latest_scan) {
      state.activeScanId = cfg.latest_scan.id;
      if (cfg.latest_scan.status === "running" || cfg.latest_scan.status === "queued") {
        setScanState(`Scan ${cfg.latest_scan.status}`, true);
        startPolling(state.activeScanId);
      } else {
        updateMeta(cfg.latest_scan);
        if (cfg.latest_scan.status === "completed") {
          await loadPath(state.currentPath);
        }
      }
    }
  }

  async function runScan() {
    disableScanButton(true);
    setScanState("Starting scan...", true);

    try {
      const result = await apiPost("/api/v1/scans");
      state.activeScanId = result.scan_id;
      setScanState(`Scan #${state.activeScanId} running`, true);
      startPolling(state.activeScanId);
    } catch (err) {
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
        updateMeta(scan);

        if (scan.status === "completed") {
          setScanState(`Completed (warnings: ${scan.warning_count})`);
          disableScanButton(false);
          state.pollingHandle = null;
          state.currentPath = state.config.analyze_root;
          await loadPath(state.currentPath);
          return;
        }

        if (scan.status === "failed") {
          setScanState(`Failed: ${scan.error || "unknown error"}`);
          disableScanButton(false);
          state.pollingHandle = null;
          return;
        }

        if (scan.progress) {
          setScanState(`Scanning ${scan.progress.scanned_nodes} items (${formatBytes(scan.progress.scanned_bytes)})`, true);
        } else {
          setScanState(`Scan ${scan.status}`, true);
        }
        state.pollingHandle = setTimeout(poll, 900);
      } catch (err) {
        setScanState(`Status error: ${err.message}`);
        disableScanButton(false);
        state.pollingHandle = null;
      }
    };

    state.pollingHandle = setTimeout(poll, 250);
  }

  async function loadPath(path) {
    if (!state.activeScanId) {
      return;
    }

    const [children, largest] = await Promise.all([
      apiGet(`/api/v1/scans/${state.activeScanId}/children?path=${encodeURIComponent(path)}`),
      apiGet(`/api/v1/scans/${state.activeScanId}/largest?path=${encodeURIComponent(path)}&limit=100`),
    ]);

    state.currentPath = children.path;
    renderBreadcrumb(children.path);
    renderTreemap(children);
    renderLargest(largest.items);
  }

  function renderTreemap(childrenResponse) {
    els.chart.innerHTML = "";

    const items = childrenResponse.children || [];
    if (items.length === 0) {
      els.chartHint.hidden = false;
      els.chartHint.textContent = "No child items at this path.";
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
        const percent = childrenResponse.total_bytes > 0
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

    for (const item of items) {
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

    els.scanMeta.textContent = `Scan #${scan.id} | status: ${scan.status} | started: ${started} | finished: ${finished} | nodes: ${scan.total_nodes} | total: ${formatBytes(scan.total_bytes)}`;
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
})();
