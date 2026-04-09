window.DiskTreemapApp = (() => {
  const sortOptions = [
    { value: "size_desc", label: "Largest first" },
    { value: "size_asc", label: "Smallest first" },
    { value: "name_asc", label: "Name A-Z" },
    { value: "name_desc", label: "Name Z-A" },
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
    if (path.length <= 72) {
      return path;
    }
    return `${path.slice(0, 20)}...${path.slice(-44)}`;
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

  function formatCount(value) {
    return Number(value || 0).toLocaleString();
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

  function updateUrl(pathname, params, replace = true) {
    const url = buildUrl(pathname, params);
    if (replace) {
      window.history.replaceState({}, "", url);
      return;
    }
    window.history.pushState({}, "", url);
  }

  function replaceUrl(pathname, params) {
    updateUrl(pathname, params, true);
  }

  function pushUrl(pathname, params) {
    updateUrl(pathname, params, false);
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

  async function apiRequest(url, options = {}) {
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

  async function apiGet(url, options = {}) {
    return apiRequest(url, { ...options, method: "GET" });
  }

  async function apiPost(url, options = {}) {
    return apiRequest(url, { ...options, method: "POST" });
  }

  function renderItemList(container, items, options) {
    const { activePath, onFocus, onNavigate } = options;
    clearChildren(container);

    items.forEach((item) => {
      const row = document.createElement("div");
      row.className = "item-row";
      row.dataset.type = item.type;
      if (item.path === activePath) {
        row.dataset.active = "true";
      }

      const main = document.createElement("div");
      main.className = "item-main";

      const titleButton = document.createElement("button");
      titleButton.type = "button";
      titleButton.className = "item-title";
      titleButton.textContent = item.name;
      titleButton.title = item.path;
      titleButton.addEventListener("click", () => {
        onFocus(item);
        if (item.type === "dir") {
          onNavigate(item.path, item.name);
        }
      });

      const subtext = document.createElement("div");
      subtext.className = "item-subtext";
      subtext.textContent = shortPath(item.path);
      subtext.title = item.path;

      const metadata = document.createElement("div");
      metadata.className = "item-metadata";

      const type = document.createElement("span");
      type.className = "item-type";
      type.dataset.type = item.type;
      type.textContent = item.type === "dir" ? "Folder" : "File";

      const size = document.createElement("span");
      size.className = "item-size";
      size.textContent = formatBytes(item.size_bytes || 0);

      metadata.append(type, size);

      main.append(titleButton, subtext);
      row.append(main, metadata);

      if (item.type === "dir") {
        const action = document.createElement("button");
        action.type = "button";
        action.className = "item-action";
        action.textContent = "Open";
        action.addEventListener("click", () => onNavigate(item.path, item.name));
        row.appendChild(action);
      } else {
        const spacer = document.createElement("div");
        spacer.className = "item-action item-action--placeholder";
        spacer.setAttribute("aria-hidden", "true");
        row.appendChild(spacer);
      }

      container.appendChild(row);
    });
  }

  function renderTreemap(container, tooltipEl, treeData, options) {
    clearChildren(container);
    hideTooltip(tooltipEl);

    if (!window.d3 || !treeData) {
      return;
    }

    const width = Math.max(container.clientWidth, 320);
    const height = Math.max(container.clientHeight, 320);
    const root = prepareTreemapHierarchy(treeData, width, height);

    const total = root.value || 0;
    const nodes = root
      .descendants()
      .filter((node) => node.depth > 0 && node.x1 - node.x0 >= 3 && node.y1 - node.y0 >= 3);

    const svg = d3
      .select(container)
      .append("svg")
      .attr("width", width)
      .attr("height", height)
      .attr("viewBox", `0 0 ${width} ${height}`)
      .attr("role", "img")
      .attr("aria-label", "Treemap of the current folder");

    const node = svg
      .selectAll("g")
      .data(nodes)
      .enter()
      .append("g")
      .attr("class", (d) => buildTreemapClassName(d, options.activePath))
      .attr("transform", (d) => `translate(${d.x0},${d.y0})`);

    node
      .append("rect")
      .attr("width", (d) => Math.max(0, d.x1 - d.x0))
      .attr("height", (d) => Math.max(0, d.y1 - d.y0))
      .attr("rx", (d) => getNodeRadius(d))
      .attr("ry", (d) => getNodeRadius(d))
      .attr("tabindex", 0)
      .on("mousemove", (event, d) => showTooltip(tooltipEl, event.clientX, event.clientY, buildTreemapTooltip(d, total)))
      .on("mouseleave", () => hideTooltip(tooltipEl))
      .on("click", (_, d) => {
        options.onFocus(d.data);
        if (d.data.clickable) {
          options.onNavigate(d.data.path, d.data.name);
        }
      })
      .on("keydown", (event, d) => {
        if (event.key !== "Enter" && event.key !== " ") {
          return;
        }
        event.preventDefault();
        options.onFocus(d.data);
        if (d.data.clickable) {
          options.onNavigate(d.data.path, d.data.name);
        }
      });

    node
      .filter((d) => shouldShowLabel(d))
      .append("text")
      .attr("class", (d) => `node-label node-label--depth-${Math.min(d.depth, 3)}`)
      .attr("x", 10)
      .attr("y", (d) => (hasNodeHeader(d) ? 17 : 15))
      .text((d) => truncateLabel(d.data.name, d.x1 - d.x0, d.depth === 1 ? 12 : 11));

    node
      .filter((d) => shouldShowMeta(d))
      .append("text")
      .attr("class", (d) => `node-meta node-meta--depth-${Math.min(d.depth, 3)}`)
      .attr("x", 10)
      .attr("y", (d) => (hasNodeHeader(d) ? 34 : 29))
      .text((d) => {
        if (d.data.synthetic) {
          return `${formatCount(d.data.hidden_item_count || 0)} hidden`;
        }
        return formatBytes(d.data.size_bytes || 0);
      });
  }

  function buildTreemapClassName(node, activePath) {
    const classes = ["node", `node--${node.data.type || "file"}`, `node--depth-${Math.min(node.depth, 3)}`];
    if (node.data.clickable) {
      classes.push("node--interactive");
    }
    if (node.data.synthetic) {
      classes.push("node--synthetic");
    }
    if (activePath && node.data.path === activePath) {
      classes.push("node--active");
    }
    return classes.join(" ");
  }

  function buildTreemapTooltip(node, total) {
    const lines = [`<strong>${escapeHtml(node.data.name)}</strong>`];
    if (node.data.path) {
      lines.push(escapeHtml(shortPath(node.data.path)));
    }

    const sizeText = `${formatBytes(node.data.size_bytes || 0)} (${formatPercent(node.value || 0, total)})`;
    lines.push(sizeText);

    if (node.data.synthetic) {
      lines.push(`${formatCount(node.data.hidden_item_count || 0)} hidden items`);
    } else {
      lines.push(node.data.type === "dir" ? "Folder" : "File");
    }

    return lines.join("<br>");
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
    return width >= 82 && height >= 26;
  }

  function shouldShowMeta(node) {
    const width = node.x1 - node.x0;
    const height = node.y1 - node.y0;
    return width >= 112 && height >= 44;
  }

  function truncateLabel(label, width, fontSize) {
    const maxChars = Math.max(5, Math.floor((width - 18) / (fontSize * 0.58)));
    if (label.length <= maxChars) {
      return label;
    }
    return `${label.slice(0, maxChars - 1)}...`;
  }

  function computeTreemapValue(item) {
    if (Array.isArray(item.children) && item.children.length > 0) {
      return 0;
    }
    return Math.max(Number(item.size_bytes || 0), 1);
  }

  function prepareTreemapHierarchy(treeData, width, height) {
    const preparedData = cloneTreemapNode(treeData);
    let root = buildTreemapHierarchy(preparedData, width, height);
    let changed = false;

    root.descendants().forEach((node) => {
      if (!Array.isArray(node.data.children) || node.data.children.length === 0 || node.depth === 0) {
        return;
      }

      if (!shouldExpandNode(node)) {
        node.data.children = [];
        changed = true;
        return;
      }

      if (groupTinyChildren(node)) {
        changed = true;
      }
    });

    if (changed) {
      root = buildTreemapHierarchy(preparedData, width, height);
    }

    return root;
  }

  function buildTreemapHierarchy(treeData, width, height) {
    const root = d3
      .hierarchy(treeData)
      .sum((item) => computeTreemapValue(item))
      .sort((a, b) => (b.value || 0) - (a.value || 0));

    d3
      .treemap()
      .tile(d3.treemapResquarify)
      .size([width, height])
      .paddingOuter(6)
      .paddingInner((node) => (node.depth <= 1 ? 4 : 2))
      .paddingTop((node) => getTreemapHeaderPadding(node))
      .round(true)(root);

    return root;
  }

  function cloneTreemapNode(node) {
    return {
      ...node,
      children: Array.isArray(node.children) ? node.children.map(cloneTreemapNode) : undefined,
    };
  }

  function shouldExpandNode(node) {
    const width = node.x1 - node.x0;
    const height = node.y1 - node.y0;
    const area = width * height;
    const shortestSide = Math.min(width, height);

    if (node.depth === 1) {
      return area >= 32000 && shortestSide >= 150 && width >= 170;
    }

    return area >= 22000 && shortestSide >= 120;
  }

  function groupTinyChildren(node) {
    const width = node.x1 - node.x0;
    const height = node.y1 - node.y0;
    const nodeArea = width * height;
    const parentSize = Math.max(Number(node.data.size_bytes || 0), 1);
    const children = Array.isArray(node.data.children) ? node.data.children : [];

    if (children.length < 4 || nodeArea < 26000) {
      return false;
    }

    const groupedChildren = [];
    let hiddenCount = 0;
    let hiddenBytes = 0;

    children.forEach((child, index) => {
      if (child.synthetic) {
        groupedChildren.push(child);
        return;
      }

      const childSize = Math.max(Number(child.size_bytes || 0), 1);
      const estimatedArea = (childSize / parentSize) * nodeArea;
      const keepBecauseLarge = index < 2 || estimatedArea >= 2600 || childSize / parentSize >= 0.09;

      if (keepBecauseLarge) {
        groupedChildren.push(child);
        return;
      }

      hiddenCount += 1;
      hiddenBytes += childSize;
    });

    if (hiddenCount === 0) {
      return false;
    }

    groupedChildren.push({
      name: hiddenCount === 1 ? "1 smaller item" : `${hiddenCount} smaller items`,
      type: "group",
      size_bytes: hiddenBytes,
      clickable: false,
      synthetic: true,
      hidden_item_count: hiddenCount,
    });

    node.data.children = groupedChildren;
    return true;
  }

  function hasNodeHeader(node) {
    return Boolean(node.children?.length) && getTreemapHeaderPadding(node) > 0;
  }

  function getTreemapHeaderPadding(node) {
    const width = node.x1 - node.x0;
    const height = node.y1 - node.y0;
    if (!node.children?.length || node.data.type !== "dir") {
      return 0;
    }
    if (width < 96 || height < 54) {
      return 0;
    }
    return node.depth === 1 ? 24 : 20;
  }

  function getNodeRadius(node) {
    const width = node.x1 - node.x0;
    const height = node.y1 - node.y0;
    const base = node.depth === 1 ? 14 : 10;
    return Math.max(3, Math.min(base, width / 7, height / 7));
  }

  return {
    apiGet,
    apiPost,
    basename,
    buildBreadcrumb,
    buildScanSummaryText,
    clearChildren,
    debounce,
    escapeHtml,
    formatBytes,
    formatCount,
    formatElapsed,
    formatPercent,
    getStatusChip,
    isScanActive,
    logScanWarnings,
    parsePositiveInt,
    pushUrl,
    readExploreUrlState,
    renderBreadcrumb,
    renderItemList,
    renderStatusChip,
    renderTreemap,
    replaceUrl,
    shortPath,
    sortOptions,
  };
})();
