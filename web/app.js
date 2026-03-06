window.DiskTreemapApp = (() => {
  const sortOptions = [
    { value: "size_desc", label: "Size desc" },
    { value: "size_asc", label: "Size asc" },
    { value: "name_asc", label: "Name asc" },
    { value: "name_desc", label: "Name desc" },
  ];

  function clearChildren(element) {
    element.innerHTML = "";
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
    const parts = cleaned.split(/[\\/]/g);
    return parts[parts.length - 1] || path;
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

  function formatPercent(part, total) {
    if (!total) {
      return "0.00%";
    }
    return `${((part / total) * 100).toFixed(2)}%`;
  }

  function escapeHtml(value) {
    return String(value)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#39;");
  }

  function isScanActive(scan) {
    return scan?.status === "running" || scan?.status === "queued";
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
    return { stateName: "idle", text: scan.status || "Idle" };
  }

  function renderStatusChip(element, scan) {
    const { stateName, text } = getStatusChip(scan);
    element.dataset.state = stateName;
    element.textContent = text;
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
    return scan.status || "Unknown";
  }

  function buildBreadcrumb(root, current) {
    if (!root) {
      return [];
    }
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

  function renderBreadcrumb(container, parts, onNavigate) {
    clearChildren(container);
    parts.forEach((part, index) => {
      const isCurrent = index === parts.length - 1;
      const element = document.createElement(isCurrent ? "span" : "button");
      element.className = isCurrent ? "breadcrumb-current" : "breadcrumb-button";
      element.textContent = part.label;
      element.title = part.path;

      if (!isCurrent) {
        element.type = "button";
        element.addEventListener("click", () => onNavigate(part.path, part.label));
      }

      container.appendChild(element);
    });
  }

  function debounce(fn, wait) {
    let timeout = null;
    return (...args) => {
      clearTimeout(timeout);
      timeout = window.setTimeout(() => fn(...args), wait);
    };
  }

  function buildUrl(pathname, params) {
    const query = new URLSearchParams();
    Object.entries(params || {}).forEach(([key, value]) => {
      if (value === null || value === undefined || value === "" || value === false) {
        return;
      }
      query.set(key, String(value));
    });
    const queryString = query.toString();
    return queryString ? `${pathname}?${queryString}` : pathname;
  }

  function replaceUrl(pathname, params) {
    window.history.replaceState({}, "", buildUrl(pathname, params));
  }

  function readExploreUrlState() {
    const params = new URLSearchParams(window.location.search);
    return {
      scanId: parsePositiveInt(params.get("scan")),
      path: params.get("path") || null,
      q: (params.get("q") || "").trim(),
      type: (params.get("type") || "").trim(),
      minSize: Math.max(0, parsePositiveInt(params.get("min_size")) || 0),
      sort: (params.get("sort") || "").trim(),
    };
  }

  function logScanWarnings(scan) {
    const warnings = Number(scan?.warning_count || 0);
    if (warnings <= 0) {
      return;
    }
    console.warn(`Scan #${scan.id} completed with ${warnings} warning(s). Check server logs for details.`);
  }

  async function apiRequest(url, options) {
    const response = await fetch(url, options);
    if (response.status === 204) {
      return null;
    }

    let body = null;
    try {
      body = await response.json();
    } catch {
      body = null;
    }

    if (!response.ok) {
      throw new Error(body?.error || `${options.method || "GET"} ${url} failed`);
    }

    return body;
  }

  async function apiGet(url) {
    return apiRequest(url, { method: "GET" });
  }

  async function apiPost(url) {
    return apiRequest(url, { method: "POST" });
  }

  function renderLargestItems(container, items, onNavigate) {
    clearChildren(container);
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
        nameEl.addEventListener("click", () => onNavigate(item.path, item.name));
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
      container.appendChild(row);
    });
  }

  function renderTreemap(container, tooltipEl, view, onNavigate) {
    clearChildren(container);
    if (!window.d3) {
      return;
    }

    const width = Math.max(container.clientWidth, 320);
    const height = Math.max(container.clientHeight, 320);
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
      .select(container)
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
        onNavigate(d.data.path, d.data.name);
      })
      .on("mousemove", (event, d) => showTooltip(tooltipEl, event.clientX, event.clientY, d.data.tooltip))
      .on("mouseleave", () => hideTooltip(tooltipEl));

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

  function showTooltip(tooltipEl, x, y, html) {
    tooltipEl.hidden = false;
    tooltipEl.innerHTML = html;

    const offset = 14;
    const rect = tooltipEl.getBoundingClientRect();
    const maxLeft = window.innerWidth - rect.width - 12;
    const maxTop = window.innerHeight - rect.height - 12;
    const left = Math.min(x + offset, Math.max(12, maxLeft));
    const top = Math.min(y + offset, Math.max(12, maxTop));

    tooltipEl.style.left = `${left}px`;
    tooltipEl.style.top = `${top}px`;
  }

  function hideTooltip(tooltipEl) {
    tooltipEl.hidden = true;
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

  return {
    sortOptions,
    apiGet,
    apiPost,
    basename,
    buildBreadcrumb,
    buildScanSummaryText,
    clearChildren,
    debounce,
    escapeHtml,
    formatBytes,
    formatElapsed,
    formatPercent,
    getStatusChip,
    isScanActive,
    logScanWarnings,
    parsePositiveInt,
    readExploreUrlState,
    renderBreadcrumb,
    renderLargestItems,
    renderStatusChip,
    renderTreemap,
    replaceUrl,
    shortPath,
  };
})();
