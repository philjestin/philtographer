(async function () {
  const status = document.getElementById('status');
  const stageEl = document.getElementById('stage');
  const headerEl = document.querySelector('header');
  const searchInput = document.getElementById('search');
  const suggestionsEl = document.getElementById('suggestions');
  const depthInput = document.getElementById('depth');
  const directionSelect = document.getElementById('direction');
  const isolateBtn = document.getElementById('isolate');
  const subgraphBtn = document.getElementById('subgraph');
  const resetBtn = document.getElementById('reset');
  const minDegreeInput = document.getElementById('minDegree');
  const toggleLabels = document.getElementById('toggleLabels');
  const hideNonFocused = document.getElementById('hideNonFocused');
  const layoutTreeBtn = document.getElementById('layoutTree');
  const layoutForceBtn = document.getElementById('layoutForce');
  const fitViewBtn = document.getElementById('fitView');
  const themeToggle = document.getElementById('themeToggle');
  const tooltip = document.getElementById('tooltip');
  const changedList = document.getElementById('changedList');
  const impactedList = document.getElementById('impactedList');
  const viewsList = document.getElementById('viewsList');
  const resizer = document.getElementById('resizer');

  const hasPixi = typeof PIXI !== 'undefined';
  const Viewport = (typeof pixi_viewport !== 'undefined' && pixi_viewport.Viewport) || (PIXI && PIXI.Viewport);

  function getSize() {
    const headerH = headerEl ? headerEl.offsetHeight : 0;
    const height = (window.innerHeight || document.documentElement.clientHeight || 600) - headerH;
    // Use the actual width of the stage container so the canvas never overlaps the sidebar
    const stageW = (stageEl && stageEl.clientWidth) ? stageEl.clientWidth : ((window.innerWidth || document.documentElement.clientWidth || 800));
    return { width: Math.max(200, stageW), height: Math.max(200, height) };
  }

  const initSize = getSize();

  status.textContent = 'Loading graph.json…';

  let graph;
  try {
    const res = await fetch('/graph.json', { cache: 'no-cache' });
    if (!res.ok) throw new Error(String(res.status));
    graph = await res.json();
  } catch (err) {
    status.textContent = 'Failed to load graph.json';
    console.error(err);
    return;
  }

  const isYaml = (id) => /\.ya?ml$/i.test(id);
  const isTest = (id) => /(^|\/)__(tests|spec)s?__(\/|$)/i.test(id) || /\.(test|spec)\.(tsx?|jsx?)$/i.test(id) || /enzyme\.test\.(tsx?|jsx?)$/i.test(id);

  function computeFiltered() {
    const nodesAll = (graph.nodes || []);
    const degree = new Map();
    for (const id of nodesAll) degree.set(id, 0);
    for (const e of (graph.edges || [])) { degree.set(e.From, (degree.get(e.From) || 0) + 1); degree.set(e.To, (degree.get(e.To) || 0) + 1); }
    const minDeg = Math.max(0, parseInt(minDegreeInput?.value || '0', 10));
    const allowed = new Set(nodesAll.filter((id) => !isYaml(id) && !isTest(id) && (degree.get(id) || 0) >= minDeg));
    const nodes = Array.from(allowed).map((id) => ({ id }));
    const idToNode = new Map(nodes.map((n) => [n.id, n]));
    const links = [];
    for (const e of (graph.edges || [])) { const s = idToNode.get(e.From); const t = idToNode.get(e.To); if (s && t) links.push({ source: s, target: t }); }
    return { nodes, links };
  }

  let full = computeFiltered();
  let nodes = full.nodes;
  let links = full.links;
  status.textContent = `Nodes: ${nodes.length}, Edges: ${links.length}`;

  const outAdj = new Map();
  const inAdj = new Map();
  function rebuildAdjacency() {
    outAdj.clear(); inAdj.clear();
    for (const n of nodes) { outAdj.set(n.id, new Set()); inAdj.set(n.id, new Set()); }
    for (const l of links) { outAdj.get(l.source.id).add(l.target.id); inAdj.get(l.target.id).add(l.source.id); }
  }
  rebuildAdjacency();

  let { width, height } = initSize;
  let simulation = d3.forceSimulation(nodes)
    .force('link', d3.forceLink(links).distance(40).strength(0.06))
    .force('charge', d3.forceManyBody().strength(-60))
    .force('center', d3.forceCenter(width / 2, height / 2))
    .force('collide', d3.forceCollide(10));

  if (!hasPixi || !Viewport) { status.textContent = 'WebGL libraries failed to load'; return; }

  const app = new PIXI.Application({ width, height, antialias: false, background: 0x0b0e14, resolution: window.devicePixelRatio || 1, autoDensity: true });
  stageEl.innerHTML = ''; stageEl.appendChild(app.view);

  const viewport = new Viewport({ screenWidth: width, screenHeight: height, worldWidth: width, worldHeight: height, events: app.renderer.events });
  app.stage.addChild(viewport); viewport.drag().wheel().pinch().decelerate();

  const edgesLayer = new PIXI.Graphics();
  const nodesLayer = new PIXI.Container();
  const labelsLayer = new PIXI.Container();
  viewport.addChild(edgesLayer); viewport.addChild(nodesLayer); viewport.addChild(labelsLayer);

  const nodeSprite = new Map();
  const nodeLabel = new Map();
  const baseColors = [0x1f77b4,0xff7f0e,0x2ca02c,0xd62728,0x9467bd,0x8c564b,0xe377c2,0x7f7f7f,0xbcbd22,0x17becf];

  let selectedId = null;

  function showTooltip(text, x, y) { tooltip.textContent = text; tooltip.style.left = `${x + 10}px`; tooltip.style.top = `${y + 10}px`; tooltip.style.display = 'block'; }
  function hideTooltip() { tooltip.style.display = 'none'; }

  function createScene() {
    edgesLayer.clear(); nodesLayer.removeChildren(); labelsLayer.removeChildren(); nodeSprite.clear?.(); nodeLabel.clear?.(); nodeSprite.forEach((_, k) => nodeSprite.delete(k)); nodeLabel.forEach((_, k) => nodeLabel.delete(k));
    for (let i = 0; i < nodes.length; i++) {
      const n = nodes[i]; const color = baseColors[i % baseColors.length]; const g = new PIXI.Graphics();
      g.beginFill(color).drawCircle(0, 0, 3.5).endFill(); g.eventMode = 'static'; g.cursor = 'pointer';
      g.on('pointerdown', () => { selectedId = n.id; focusOn(n.id); highlightSelected(); });
      g.on('pointerover', (ev) => { showTooltip(n.id, ev.clientX, ev.clientY); }); g.on('pointermove', (ev) => { showTooltip(n.id, ev.clientX, ev.clientY); }); g.on('pointerout', hideTooltip);
      nodesLayer.addChild(g); nodeSprite.set(n.id, g);
      const label = new PIXI.Text(labelFor(n.id), { fontSize: 10, fill: 0xe6e6e6, resolution: 2 }); label.anchor.set(0, 0.5); labelsLayer.addChild(label); nodeLabel.set(n.id, label);
    }
    toggleLabelVisibility(); highlightSelected();
  }
  createScene();

  // Prime the sidebar once on load using the latest events (if any)
  try {
    const r0 = await fetch('/events.json', { cache: 'no-cache' });
    if (r0.ok) {
      const e0 = await r0.json();
      if (e0 && typeof e0.ts === 'number') {
        lastTs = e0.ts;
        renderDiff(e0.changed, e0.impacted);
      }
    }
  } catch {}

  function highlightSelected() { for (const [id, sprite] of nodeSprite) { sprite.lineStyle?.(0); if (id === selectedId) { sprite.lineStyle?.(1.5, 0x000000, 1); } } }
  function toggleLabelVisibility() { const on = !!toggleLabels?.checked; labelsLayer.visible = on; }
  toggleLabels?.addEventListener('change', toggleLabelVisibility); toggleLabelVisibility();
  function labelFor(id) { const idx = id.lastIndexOf('/'); return idx >= 0 ? id.slice(idx + 1) : id; }

  let lastEdgeDraw = 0;
  function drawEdges(alphaAll) {
    const now = performance.now();
    if (now - lastEdgeDraw < 16) return; // ~60fps throttle
    lastEdgeDraw = now;
    edgesLayer.clear(); edgesLayer.lineStyle(0.6, 0x5b6472, alphaAll ?? 0.28);
    for (const l of links) { edgesLayer.moveTo(l.source.x, l.source.y); edgesLayer.lineTo(l.target.x, l.target.y); }
  }
  simulation.on('tick', () => { for (const n of nodes) { const s = nodeSprite.get(n.id); if (s) s.position.set(n.x, n.y); const t = nodeLabel.get(n.id); if (t) t.position.set(n.x + 8, n.y); } drawEdges(); });

  function bfsDirectional(startId, dir) {
    const visited = new Set([startId]);
    let frontier = new Set([startId]);
    while (frontier.size) {
      const next = new Set();
      for (const id of frontier) {
        if (dir !== 'in') for (const n of outAdj.get(id) || []) if (!visited.has(n)) { visited.add(n); next.add(n); }
        if (dir !== 'out') for (const n of inAdj.get(id) || []) if (!visited.has(n)) { visited.add(n); next.add(n); }
      }
      frontier = next;
    }
    return visited;
  }

  function bfs(startId, options) { const { maxDepth, direction } = options; const visited = new Set([startId]); let frontier = new Set([startId]); for (let depth = 0; depth < maxDepth; depth++) { const next = new Set(); for (const id of frontier) { if (direction !== 'in') for (const n of outAdj.get(id) || []) if (!visited.has(n)) { visited.add(n); next.add(n); } if (direction !== 'out') for (const n of inAdj.get(id) || []) if (!visited.has(n)) { visited.add(n); next.add(n); } } if (next.size === 0) break; frontier = next; } return visited; }

  function applyFocus(keep) { const hide = !!hideNonFocused?.checked; for (const n of nodes) { const visible = keep.has(n.id) || !hide; const alpha = keep.has(n.id) ? 1 : (hide ? 0 : 0.2); const s = nodeSprite.get(n.id); if (s) { s.alpha = alpha; s.renderable = visible; } const t = nodeLabel.get(n.id); if (t) { t.alpha = alpha; t.renderable = visible && labelsLayer.visible; } } edgesLayer.clear(); for (const l of links) { const show = keep.has(l.source.id) && keep.has(l.target.id); const alpha = show ? (hide ? 0.6 : 0.35) : (hide ? 0 : 0.05); if (alpha <= 0) continue; edgesLayer.lineStyle(0.6, 0x5b6472, alpha); edgesLayer.moveTo(l.source.x, l.source.y); edgesLayer.lineTo(l.target.x, l.target.y); } }

  function focusOn(startId) { const maxDepth = Math.max(0, parseInt(depthInput?.value || '2', 10)); const direction = directionSelect?.value || 'both'; applyFocus(bfs(startId, { maxDepth, direction })); }

  function resetFocus() { nodes = full.nodes; links = full.links; status.textContent = `Nodes: ${nodes.length}, Edges: ${links.length}`; rebuildAdjacency(); simulation.nodes(nodes); simulation.force('link').links(links); simulation.alpha(0.5).restart(); createScene(); }

  isolateBtn?.addEventListener('click', () => { const q = (searchInput?.value || '').trim(); if (!q) return; let match = nodes.find((n) => n.id === q) || nodes.find((n) => n.id.includes(q)); if (match) { selectedId = match.id; highlightSelected(); focusOn(match.id); } suggestionsEl?.classList.remove('show'); });

  // Autosuggest
  let activeIndex = -1;
  function labelFor(id) { const idx = id.lastIndexOf('/'); return idx >= 0 ? id.slice(idx + 1) : id; }
  function rankCandidates(query) {
    const q = query.toLowerCase(); if (!q) return [];
    const scored = nodes.map((n) => { const id = n.id; const name = labelFor(id).toLowerCase(); const hay = id.toLowerCase(); let score = -1; if (name.startsWith(q)) score = 100 - name.length; else if (hay.includes(q)) score = 50 - hay.length; return { id, score }; }).filter(x => x.score >= 0);
    scored.sort((a,b)=> b.score - a.score);
    return scored.slice(0, 20).map(s => s.id);
  }
  function renderSuggestions(ids) {
    if (!suggestionsEl) return;
    suggestionsEl.innerHTML = '';
    if (!ids.length) { suggestionsEl.classList.remove('show'); activeIndex = -1; return; }
    for (let i=0;i<ids.length;i++) { const li = document.createElement('li'); li.textContent = ids[i]; li.setAttribute('role','option'); li.addEventListener('mousedown', (e) => { e.preventDefault(); chooseSuggestion(ids[i]); }); suggestionsEl.appendChild(li); }
    suggestionsEl.classList.add('show'); activeIndex = -1;
  }
  function chooseSuggestion(id) { searchInput.value = id; suggestionsEl?.classList.remove('show'); const match = nodes.find(n => n.id === id); if (match) { selectedId = id; highlightSelected(); focusOn(id); } }
  searchInput?.addEventListener('input', () => { const q = (searchInput.value || '').trim(); renderSuggestions(rankCandidates(q)); });
  searchInput?.addEventListener('keydown', (e) => {
    if (!suggestionsEl?.classList.contains('show')) return;
    const items = Array.from(suggestionsEl.querySelectorAll('li'));
    if (!items.length) return;
    if (e.key === 'ArrowDown') { e.preventDefault(); activeIndex = Math.min(items.length - 1, activeIndex + 1); updateActive(items); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); activeIndex = Math.max(0, activeIndex - 1); updateActive(items); }
    else if (e.key === 'Enter') { e.preventDefault(); if (activeIndex >= 0) chooseSuggestion(items[activeIndex].textContent); else if (items[0]) chooseSuggestion(items[0].textContent); }
    else if (e.key === 'Escape') { suggestionsEl.classList.remove('show'); }
  });
  function updateActive(items) { items.forEach((el, i) => { if (i === activeIndex) el.classList.add('active'); else el.classList.remove('active'); }); const el = items[activeIndex]; if (el) { const rect = el.getBoundingClientRect(); const parent = suggestionsEl.getBoundingClientRect(); if (rect.bottom > parent.bottom) el.scrollIntoView(false); if (rect.top < parent.top) el.scrollIntoView(); } }

  subgraphBtn?.addEventListener('click', () => {
    if (!selectedId) return;
    const maxDepth = Math.max(0, parseInt(depthInput?.value || '2', 10));
    const direction = directionSelect?.value || 'both';
    const keep = bfs(selectedId, { maxDepth, direction });
    const idToNode = new Map(nodes.map((n) => [n.id, n]));
    const nodesSub = Array.from(keep).map((id) => idToNode.get(id)).filter(Boolean);
    const linksSub = links.filter((l) => keep.has(l.source.id) && keep.has(l.target.id));
    nodes = nodesSub; links = linksSub; status.textContent = `Nodes: ${nodes.length}, Edges: ${links.length}`;
    rebuildAdjacency();
    simulation.nodes(nodes); simulation.force('link').links(links); simulation.alpha(0.7).restart();
    createScene();
  });

  // Directional layered tree layout from selectedId
  function applyTreeLayout() {
    if (!selectedId) return;
    // Use current direction for a trie-like expansion (default outbound)
    const direction = directionSelect?.value || 'out';
    const dist = new Map();
    const queue = [selectedId]; dist.set(selectedId, 0);
    while (queue.length) {
      const v = queue.shift();
      const d = dist.get(v) || 0;
      if (direction !== 'in') for (const n of outAdj.get(v) || []) if (!dist.has(n)) { dist.set(n, d + 1); queue.push(n); }
      if (direction !== 'out') for (const n of inAdj.get(v) || []) if (!dist.has(n)) { dist.set(n, d + 1); queue.push(n); }
    }
    const layers = new Map();
    for (const [id, d] of dist) { if (!layers.has(d)) layers.set(d, []); layers.get(d).push(id); }

    const layerGapY = 80, nodeGapX = 80;
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    const cx = width / 2, cy = height / 2;
    const maxDepthLocal = Math.max(...layers.keys());
    for (const [d, ids] of layers) {
      const totalWidth = Math.max(0, (ids.length - 1) * nodeGapX);
      for (let i = 0; i < ids.length; i++) {
        const id = ids[i];
        const x = cx - totalWidth / 2 + i * nodeGapX;
        const y = cy + (d - maxDepthLocal / 2) * layerGapY;
        const n = nodes.find((nn) => nn.id === id);
        if (!n) continue; n.x = x; n.y = y;
        minX = Math.min(minX, x); maxX = Math.max(maxX, x);
        minY = Math.min(minY, y); maxY = Math.max(maxY, y);
      }
    }

    // Stop simulation and render static tree
    simulation.stop();
    for (const n of nodes) { const s = nodeSprite.get(n.id); if (s) s.position.set(n.x, n.y); const t = nodeLabel.get(n.id); if (t) t.position.set(n.x + 8, n.y); }
    edgesLayer.clear(); edgesLayer.lineStyle(0.6, 0x999999, 0.35); for (const l of links) { edgesLayer.moveTo(l.source.x, l.source.y); edgesLayer.lineTo(l.target.x, l.target.y); }

    // Center viewport on laid-out subgraph
    const centerX = (minX + maxX) / 2; const centerY = (minY + maxY) / 2;
    viewport.moveCenter(centerX, centerY);
  }

  layoutTreeBtn?.addEventListener('click', applyTreeLayout);
  layoutForceBtn?.addEventListener('click', () => { simulation.alpha(0.8).restart(); });
  fitViewBtn?.addEventListener('click', () => { viewport.fit(true); });

  resetBtn?.addEventListener('click', () => { selectedId = null; resetFocus(); });

  function onResize() { const size = getSize(); width = size.width; height = size.height; app.renderer.resize(width, height); viewport.resize(width, height, width, height); simulation.force('center', d3.forceCenter(width / 2, height / 2)); simulation.alpha(0.15).restart(); }

  // theme toggle (dark default)
  function applyTheme(dark) {
    const bg = dark ? 0x0b0e14 : 0xffffff;
    app.renderer.background.color = bg;
    document.body.style.background = dark ? '#0b0e14' : '#ffffff';
  }
  themeToggle?.addEventListener('change', () => { applyTheme(!!themeToggle.checked); });
  applyTheme(true);
  window.addEventListener('resize', onResize);
  // Also react to flex layout changes (e.g., sidebar size) using a ResizeObserver on the stage container
  if (window.ResizeObserver) { const ro = new ResizeObserver(() => onResize()); ro.observe(stageEl); }

  // Drag-to-resize sidebar
  if (resizer) {
    let dragging = false;
    let startX = 0;
    let startWidth = 0;
    const sidebar = document.getElementById('sidebar');
    resizer.addEventListener('pointerdown', (e) => {
      dragging = true; startX = e.clientX; startWidth = sidebar.offsetWidth; resizer.setPointerCapture(e.pointerId);
    });
    resizer.addEventListener('pointermove', (e) => {
      if (!dragging) return; const dx = e.clientX - startX; // drag right grows sidebar
      let newWidth = Math.min(Math.max(240, startWidth + dx), Math.floor(window.innerWidth * 0.6));
      sidebar.style.width = newWidth + 'px'; onResize();
    });
    const stop = () => { dragging = false; };
    resizer.addEventListener('pointerup', stop);
    resizer.addEventListener('pointercancel', stop);
    // Also stop on leaving window
    window.addEventListener('pointerup', stop);
    window.addEventListener('pointercancel', stop);
  }

  // Live updates: poll events.json periodically (simpler than websockets for now)
  let lastTs = 0;
  // Switch to SSE: listen for /sse updates and then fetch events+graph
  async function refreshFromServer() {
    try {
      const r = await fetch('/events.json', { cache: 'no-cache' });
      if (!r.ok) return; const evt = await r.json(); if (!evt || typeof evt.ts !== 'number' || evt.ts <= lastTs) return; lastTs = evt.ts;
      const gres = await fetch('/graph.json', { cache: 'no-cache' }); if (!gres.ok) return; graph = await gres.json();
      const fullNow = computeFiltered(); nodes = fullNow.nodes; links = fullNow.links; rebuildAdjacency(); simulation.nodes(nodes); simulation.force('link').links(links); simulation.alpha(0.4).restart(); createScene(); status.textContent = `Nodes: ${nodes.length}, Edges: ${links.length}`;
      renderDiff(evt.changed, evt.impacted);
      const list = Array.isArray(evt.impacted) && evt.impacted.length ? evt.impacted : (Array.isArray(evt.changed) ? evt.changed : []);
      if (list.length) { const set = new Set(list.filter(Boolean)); applyFocus(set); selectedId = list[0]; highlightSelected(); }
    } catch (e) { console.error('update error', e); }
  }

  function connectWS() {
    try {
      const proto = (location.protocol === 'https:') ? 'wss' : 'ws';
      const ws = new WebSocket(`${proto}://${location.host}/ws`);
      // Fallback polling until the first ws message arrives
      let pollId = setInterval(() => refreshFromServer(), 2000);
      ws.onopen = () => { console.log('[ws] connected'); };
      ws.onmessage = () => {
        if (pollId) { clearInterval(pollId); pollId = null; }
        refreshFromServer();
      };
      ws.onclose = () => {
        console.warn('[ws] closed, retrying...');
        if (!pollId) { pollId = setInterval(() => refreshFromServer(), 2000); }
        setTimeout(connectWS, 1500);
      };
      ws.onerror = () => { try { ws.close(); } catch {} };
    } catch (e) {
      console.warn('[ws] connect failed', e);
      setTimeout(connectWS, 2000);
    }
  }
  connectWS();

  function renderDiff(changed, impacted) {
    const c = Array.isArray(changed) ? changed : [];
    const i = Array.isArray(impacted) ? impacted : [];
    if (changedList) {
      changedList.innerHTML = '';
      if (!c.length) { const span=document.createElement('span'); span.textContent='None'; span.style.opacity='0.7'; changedList.appendChild(span); }
      for (const f of c) { const chip = document.createElement('span'); chip.className = 'chip'; chip.textContent = f; chip.title = f; chip.addEventListener('click',()=>{ searchInput.value=f; selectedId=f; highlightSelected(); focusOn(f); }); changedList.appendChild(chip); }
    }
    if (impactedList) {
      impactedList.innerHTML = '';
      if (!i.length) { const span=document.createElement('span'); span.textContent='None'; span.style.opacity='0.7'; impactedList.appendChild(span); }
      for (const f of i) { const chip = document.createElement('span'); chip.className = 'chip'; chip.textContent = f; chip.title = f; chip.addEventListener('click',()=>{ searchInput.value=f; selectedId=f; highlightSelected(); focusOn(f); }); impactedList.appendChild(chip); }
    }
    // Views: if server provided per-root graphs, offer pills to switch
    if (viewsList) {
      viewsList.innerHTML = '';
      const hasGraphs = Array.isArray(graph.graphs) && graph.graphs.length > 0;
      if (hasGraphs) {
        const makeChip = (label, on) => { const s=document.createElement('span'); s.className='chip'; s.textContent=label; s.addEventListener('click', on); return s; };
        const unionChip = makeChip('Union', () => { applyGraphUnion(); }); unionChip.classList.add('active'); viewsList.appendChild(unionChip);
        for (const gsub of graph.graphs) {
          const chip = makeChip(gsub.root ? labelFor(gsub.root) : 'graph', () => { applyGraphSub(gsub, chip); });
          viewsList.appendChild(chip);
        }
        function clearActive() { viewsList.querySelectorAll('.chip').forEach(c=>c.classList.remove('active')); }
        function applyGraphUnion() {
          clearActive(); unionChip.classList.add('active');
          // Rebuild from union nodes/edges present in graph
          const gsave = graph; // use current graph json
          full = computeFiltered(); nodes = full.nodes; links = full.links; status.textContent = `Nodes: ${nodes.length}, Edges: ${links.length}`; rebuildAdjacency(); simulation.nodes(nodes); simulation.force('link').links(links); simulation.alpha(0.4).restart(); createScene();
        }
        function applyGraphSub(gsub, chipEl) {
          clearActive(); chipEl.classList.add('active');
          // Build nodes/links from subgraph payload
          const nodesSet = new Set(gsub.nodes || []);
          nodes = Array.from(nodesSet).map(id => ({ id }));
          const idToNode = new Map(nodes.map(n=>[n.id,n]));
          links = [];
          for (const e of gsub.edges || []) { const s = idToNode.get(e.From||e.from||e.source); const t = idToNode.get(e.To||e.to||e.target); if (s&&t) links.push({ source:s, target:t }); }
          status.textContent = `Nodes: ${nodes.length}, Edges: ${links.length}`; rebuildAdjacency(); simulation.nodes(nodes); simulation.force('link').links(links); simulation.alpha(0.5).restart(); createScene();
        }
      }
    }
  }
})();
