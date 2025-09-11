(async function () {
  const status = document.getElementById('status');
  const stageEl = document.getElementById('stage');
  const headerEl = document.querySelector('header');
  const searchInput = document.getElementById('search');
  const depthInput = document.getElementById('depth');
  const directionSelect = document.getElementById('direction');
  const isolateBtn = document.getElementById('isolate');
  const subgraphBtn = document.getElementById('subgraph');
  const resetBtn = document.getElementById('reset');
  const minDegreeInput = document.getElementById('minDegree');
  const toggleLabels = document.getElementById('toggleLabels');
  const hideNonFocused = document.getElementById('hideNonFocused');
  const tooltip = document.getElementById('tooltip');

  const hasPixi = typeof PIXI !== 'undefined';
  const Viewport = (typeof pixi_viewport !== 'undefined' && pixi_viewport.Viewport) || (PIXI && PIXI.Viewport);

  function getSize() {
    const headerH = headerEl ? headerEl.offsetHeight : 0;
    const width = window.innerWidth || document.documentElement.clientWidth || 800;
    const height = (window.innerHeight || document.documentElement.clientHeight || 600) - headerH;
    return { width, height: Math.max(200, height) };
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
  const isTest = (id) => /\.test\.(tsx?|jsx?)$/i.test(id);

  const degree = new Map();
  const nodesAll = (graph.nodes || []);
  for (const id of nodesAll) degree.set(id, 0);
  for (const e of (graph.edges || [])) { degree.set(e.From, (degree.get(e.From) || 0) + 1); degree.set(e.To, (degree.get(e.To) || 0) + 1); }

  function computeFiltered() {
    const minDeg = Math.max(0, parseInt(minDegreeInput?.value || '0', 10));
    const allowed = new Set(nodesAll.filter((id) => !isYaml(id) && !isTest(id) && (degree.get(id) || 0) >= minDeg));
    const nodes = Array.from(allowed).map((id) => ({ id }));
    const idToNode = new Map(nodes.map((n) => [n.id, n]));
    const links = [];
    for (const e of graph.edges || []) { const s = idToNode.get(e.From); const t = idToNode.get(e.To); if (s && t) links.push({ source: s, target: t }); }
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
  const simulation = d3.forceSimulation(nodes)
    .force('link', d3.forceLink(links).distance(40).strength(0.06))
    .force('charge', d3.forceManyBody().strength(-60))
    .force('center', d3.forceCenter(width / 2, height / 2))
    .force('collide', d3.forceCollide(10));

  if (!hasPixi || !Viewport) { status.textContent = 'WebGL libraries failed to load'; return; }

  const app = new PIXI.Application({ width, height, antialias: false, background: 0xfafafa, resolution: window.devicePixelRatio || 1, autoDensity: true });
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
      const label = new PIXI.Text(labelFor(n.id), { fontSize: 10, fill: 0x111111, resolution: 2 }); label.anchor.set(0, 0.5); labelsLayer.addChild(label); nodeLabel.set(n.id, label);
    }
    toggleLabelVisibility(); highlightSelected();
  }
  createScene();

  function highlightSelected() { for (const [id, sprite] of nodeSprite) { sprite.lineStyle?.(0); if (id === selectedId) { sprite.lineStyle?.(1.5, 0x000000, 1); } } }
  function toggleLabelVisibility() { const on = !!toggleLabels?.checked; labelsLayer.visible = on; }
  toggleLabels?.addEventListener('change', toggleLabelVisibility); toggleLabelVisibility();
  function labelFor(id) { const idx = id.lastIndexOf('/'); return idx >= 0 ? id.slice(idx + 1) : id; }

  function drawEdges(alphaAll) { edgesLayer.clear(); edgesLayer.lineStyle(0.6, 0x999999, alphaAll ?? 0.25); for (const l of links) { edgesLayer.moveTo(l.source.x, l.source.y); edgesLayer.lineTo(l.target.x, l.target.y); } }
  simulation.on('tick', () => { for (const n of nodes) { const s = nodeSprite.get(n.id); if (s) s.position.set(n.x, n.y); const t = nodeLabel.get(n.id); if (t) t.position.set(n.x + 8, n.y); } drawEdges(); });

  function bfs(startId, options) { const { maxDepth, direction } = options; const visited = new Set([startId]); let frontier = new Set([startId]); for (let depth = 0; depth < maxDepth; depth++) { const next = new Set(); for (const id of frontier) { if (direction !== 'in') for (const n of outAdj.get(id) || []) if (!visited.has(n)) { visited.add(n); next.add(n); } if (direction !== 'out') for (const n of inAdj.get(id) || []) if (!visited.has(n)) { visited.add(n); next.add(n); } } if (next.size === 0) break; frontier = next; } return visited; }

  function applyFocus(keep) { const hide = !!hideNonFocused?.checked; for (const n of nodes) { const visible = keep.has(n.id) || !hide; const alpha = keep.has(n.id) ? 1 : (hide ? 0 : 0.2); const s = nodeSprite.get(n.id); if (s) { s.alpha = alpha; s.renderable = visible; } const t = nodeLabel.get(n.id); if (t) { t.alpha = alpha; t.renderable = visible && labelsLayer.visible; } } edgesLayer.clear(); for (const l of links) { const show = keep.has(l.source.id) && keep.has(l.target.id); const alpha = show ? (hide ? 0.6 : 0.35) : (hide ? 0 : 0.05); if (alpha <= 0) continue; edgesLayer.lineStyle(0.6, 0x999999, alpha); edgesLayer.moveTo(l.source.x, l.source.y); edgesLayer.lineTo(l.target.x, l.target.y); } }

  function focusOn(startId) { const maxDepth = Math.max(0, parseInt(depthInput?.value || '2', 10)); const direction = directionSelect?.value || 'both'; applyFocus(bfs(startId, { maxDepth, direction })); }

  function resetFocus() { nodes = full.nodes; links = full.links; status.textContent = `Nodes: ${nodes.length}, Edges: ${links.length}`; rebuildAdjacency(); simulation.nodes(nodes); simulation.force('link').links(links); simulation.alpha(0.5).restart(); createScene(); }

  isolateBtn?.addEventListener('click', () => { const q = (searchInput?.value || '').trim(); if (!q) return; let match = nodes.find((n) => n.id === q) || nodes.find((n) => n.id.includes(q)); if (match) { selectedId = match.id; highlightSelected(); focusOn(match.id); } });

  subgraphBtn?.addEventListener('click', () => {
    if (!selectedId) return;
    const maxDepth = Math.max(0, parseInt(depthInput?.value || '2', 10));
    const direction = directionSelect?.value || 'both';
    const keep = bfs(selectedId, { maxDepth, direction });
    // Build subgraph arrays
    const idToNode = new Map(nodes.map((n) => [n.id, n]));
    const nodesSub = Array.from(keep).map((id) => idToNode.get(id)).filter(Boolean);
    const linksSub = links.filter((l) => keep.has(l.source.id) && keep.has(l.target.id));
    nodes = nodesSub; links = linksSub; status.textContent = `Nodes: ${nodes.length}, Edges: ${links.length}`;
    rebuildAdjacency();
    simulation.nodes(nodes); simulation.force('link').links(links); simulation.alpha(0.7).restart();
    createScene();
  });

  resetBtn?.addEventListener('click', () => { selectedId = null; resetFocus(); });

  function onResize() { const size = getSize(); width = size.width; height = size.height; app.renderer.resize(width, height); viewport.resize(width, height, width, height); simulation.force('center', d3.forceCenter(width / 2, height / 2)); simulation.alpha(0.15).restart(); }
  window.addEventListener('resize', onResize);
})();
