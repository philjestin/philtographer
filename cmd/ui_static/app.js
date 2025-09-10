(async function () {
  const status = document.getElementById('status');
  const svg = d3.select('#graph');
  const headerEl = document.querySelector('header');
  const searchInput = document.getElementById('search');
  const depthInput = document.getElementById('depth');
  const directionSelect = document.getElementById('direction');
  const isolateBtn = document.getElementById('isolate');
  const resetBtn = document.getElementById('reset');

  function getSize() {
    const headerH = headerEl ? headerEl.offsetHeight : 0;
    const width = window.innerWidth || document.documentElement.clientWidth || 800;
    const height = (window.innerHeight || document.documentElement.clientHeight || 600) - headerH;
    return { width, height: Math.max(200, height) };
  }

  // Initialize SVG size to viewport
  const initSize = getSize();
  svg.attr('width', initSize.width).attr('height', initSize.height);

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

  const nodes = (graph.nodes || []).map((id) => ({ id }));
  const edgesRaw = graph.edges || [];

  const idToNode = new Map(nodes.map((n) => [n.id, n]));
  const links = [];
  for (const e of edgesRaw) {
    const s = idToNode.get(e.From);
    const t = idToNode.get(e.To);
    if (s && t) links.push({ source: s, target: t });
  }

  status.textContent = `Nodes: ${nodes.length}, Edges: ${links.length}`;

  // Build adjacency for focus filtering
  const outAdj = new Map();
  const inAdj = new Map();
  for (const n of nodes) {
    outAdj.set(n.id, new Set());
    inAdj.set(n.id, new Set());
  }
  for (const l of links) {
    outAdj.get(l.source.id).add(l.target.id);
    inAdj.get(l.target.id).add(l.source.id);
  }

  // Build simulation with initial size
  let { width, height } = initSize;
  const simulation = d3.forceSimulation(nodes)
    .force('link', d3.forceLink(links).distance(40).strength(0.03))
    .force('charge', d3.forceManyBody().strength(-80))
    .force('center', d3.forceCenter(width / 2, height / 2))
    .force('collide', d3.forceCollide(10));

  const g = svg.append('g');

  const link = g.append('g')
    .selectAll('line')
    .data(links)
    .join('line')
    .attr('class', 'link')
    .attr('stroke', '#999')
    .attr('stroke-opacity', 0.35)
    .attr('stroke-width', 0.7)
    .attr('vector-effect', 'non-scaling-stroke');

  const node = g.append('g')
    .selectAll('g')
    .data(nodes)
    .join('g')
    .attr('class', 'node')
    .on('click', (_, d) => focusOn(d.id))
    .call(d3.drag()
      .subject((event, d) => d)
      .on('start', (event, d) => {
        if (!event.active) simulation.alphaTarget(0.3).restart();
        d.fx = d.x;
        d.fy = d.y;
      })
      .on('drag', (event, d) => {
        d.fx = event.x;
        d.fy = event.y;
      })
      .on('end', (event, d) => {
        if (!event.active) simulation.alphaTarget(0);
        d.fx = null;
        d.fy = null;
      }));

  node.append('circle')
    .attr('r', 4.5)
    .attr('fill', (d, i) => d3.schemeCategory10[i % 10]);

  node.append('title').text((d) => d.id);

  node.append('text')
    .attr('x', 8)
    .attr('y', '0.31em')
    .text((d) => labelFor(d.id));

  function labelFor(id) {
    const idx = id.lastIndexOf('/');
    return idx >= 0 ? id.slice(idx + 1) : id;
  }

  simulation.on('tick', () => {
    link
      .attr('x1', (d) => d.source.x)
      .attr('y1', (d) => d.source.y)
      .attr('x2', (d) => d.target.x)
      .attr('y2', (d) => d.target.y);

    node.attr('transform', (d) => `translate(${d.x},${d.y})`);
  });

  // Zoom/pan
  const zoom = d3.zoom().on('zoom', (event) => {
    g.attr('transform', event.transform);
  });
  svg.call(zoom);

  // Focus helpers
  function bfs(startId, options) {
    const { maxDepth, direction } = options;
    const visited = new Set([startId]);
    let frontier = new Set([startId]);
    for (let depth = 0; depth < maxDepth; depth++) {
      const next = new Set();
      for (const id of frontier) {
        if (direction !== 'in') {
          for (const n of outAdj.get(id) || []) if (!visited.has(n)) { visited.add(n); next.add(n); }
        }
        if (direction !== 'out') {
          for (const n of inAdj.get(id) || []) if (!visited.has(n)) { visited.add(n); next.add(n); }
        }
      }
      if (next.size === 0) break;
      frontier = next;
    }
    return visited;
  }

  function focusOn(startId) {
    const maxDepth = Math.max(0, parseInt(depthInput?.value || '2', 10));
    const direction = directionSelect?.value || 'both';
    const keep = bfs(startId, { maxDepth, direction });

    node.selectAll('circle')
      .attr('opacity', (d) => keep.has(d.id) ? 1 : 0.1);
    node.selectAll('text')
      .attr('opacity', (d) => keep.has(d.id) ? 1 : 0.05);
    link
      .attr('opacity', (l) => keep.has(l.source.id) && keep.has(l.target.id) ? 0.45 : 0.03);
  }

  function resetFocus() {
    node.selectAll('circle').attr('opacity', 1);
    node.selectAll('text').attr('opacity', 1);
    link.attr('opacity', 0.35);
  }

  // Wire controls
  isolateBtn?.addEventListener('click', () => {
    const q = (searchInput?.value || '').trim();
    if (!q) return;
    // Find exact or partial match
    let match = nodes.find((n) => n.id === q);
    if (!match) match = nodes.find((n) => n.id.includes(q));
    if (match) focusOn(match.id);
  });

  resetBtn?.addEventListener('click', resetFocus);

  // Handle resize to keep canvas full-viewport
  function onResize() {
    const size = getSize();
    const width = size.width;
    const height = size.height;
    svg.attr('width', width).attr('height', height);
    simulation.force('center', d3.forceCenter(width / 2, height / 2));
    simulation.alpha(0.15).restart();
  }
  window.addEventListener('resize', onResize);
})();
