(function () {
  const canvas = document.querySelector("[data-thread-field]");
  if (!canvas) {
    return;
  }

  const ctx = canvas.getContext("2d", { alpha: false });
  const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
  let width = 0;
  let height = 0;
  let dpr = 1;
  let raf = 0;
  let graph = null;

  const colors = {
    bg: "#090a08",
    line: "rgba(185, 179, 159, 0.16)",
    lineSoft: "rgba(185, 179, 159, 0.07)",
    card: "rgba(14, 16, 13, 0.50)",
    cardLine: "rgba(244, 183, 64, 0.20)",
    text: "rgba(247, 243, 232, 0.24)",
    amber: "rgba(244, 183, 64, 0.92)",
    teal: "rgba(81, 214, 204, 0.82)",
    leaf: "rgba(138, 174, 91, 0.78)"
  };

  function resize() {
    const rect = canvas.getBoundingClientRect();
    width = Math.max(1, Math.floor(rect.width));
    height = Math.max(1, Math.floor(rect.height));
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    canvas.width = Math.floor(width * dpr);
    canvas.height = Math.floor(height * dpr);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    graph = buildGraph();
    drawFrame(performance.now());
  }

  function pt(x, y) {
    return { x: Math.round(width * x), y: Math.round(height * y) };
  }

  function buildGraph() {
    const compact = width < 760;
    const cards = compact
      ? [
          card("Claude Code", 0.12, 0.18, 150, 42),
          card("Threadmark", 0.50, 0.34, 168, 46),
          card("Journal", 0.17, 0.58, 128, 40),
          card("Codex", 0.62, 0.70, 124, 40),
          card("Startup packet", 0.28, 0.82, 176, 42)
        ]
      : [
          card("Claude Code", 0.10, 0.20, 158, 44),
          card("Codex", 0.19, 0.66, 120, 42),
          card("Threadmark", 0.48, 0.33, 178, 48),
          card("Journal", 0.71, 0.20, 130, 42),
          card("Startup packet", 0.72, 0.61, 190, 44),
          card("Next session", 0.50, 0.80, 154, 42)
        ];

    const byLabel = Object.fromEntries(cards.map((item) => [item.label, item]));
    const paths = compact
      ? [
          route(byLabel["Claude Code"], byLabel["Threadmark"], 0.18),
          route(byLabel["Threadmark"], byLabel["Journal"], 0.40),
          route(byLabel["Threadmark"], byLabel["Startup packet"], 0.56),
          route(byLabel["Codex"], byLabel["Threadmark"], 0.68),
          route(byLabel["Journal"], byLabel["Startup packet"], 0.82)
        ]
      : [
          route(byLabel["Claude Code"], byLabel["Threadmark"], 0.16),
          route(byLabel["Codex"], byLabel["Threadmark"], 0.34),
          route(byLabel["Threadmark"], byLabel["Journal"], 0.48),
          route(byLabel["Threadmark"], byLabel["Startup packet"], 0.62),
          route(byLabel["Journal"], byLabel["Startup packet"], 0.78),
          route(byLabel["Startup packet"], byLabel["Next session"], 0.88)
        ];

    return { cards, paths };
  }

  function card(label, x, y, w, h) {
    const center = pt(x, y);
    return {
      label,
      x: center.x - w / 2,
      y: center.y - h / 2,
      w,
      h,
      center
    };
  }

  function route(from, to, phase) {
    const start = anchor(from, to);
    const end = anchor(to, from);
    const dx = end.x - start.x;
    const midA = { x: start.x + dx * 0.44, y: start.y };
    const midB = { x: start.x + dx * 0.44, y: end.y };
    return {
      points: [start, midA, midB, end],
      phase,
      tone: phase < 0.4 ? colors.teal : phase < 0.72 ? colors.amber : colors.leaf
    };
  }

  function anchor(card, toward) {
    const dx = toward.center.x - card.center.x;
    const dy = toward.center.y - card.center.y;
    if (Math.abs(dx) > Math.abs(dy)) {
      return {
        x: card.center.x + Math.sign(dx) * card.w / 2,
        y: card.center.y
      };
    }
    return {
      x: card.center.x,
      y: card.center.y + Math.sign(dy) * card.h / 2
    };
  }

  function drawBackground() {
    ctx.globalCompositeOperation = "source-over";
    ctx.fillStyle = colors.bg;
    ctx.fillRect(0, 0, width, height);

    ctx.strokeStyle = "rgba(255, 255, 255, 0.025)";
    ctx.lineWidth = 1;
    ctx.beginPath();
    for (let x = 0; x <= width; x += 64) {
      ctx.moveTo(x, 0);
      ctx.lineTo(x, height);
    }
    for (let y = 0; y <= height; y += 64) {
      ctx.moveTo(0, y);
      ctx.lineTo(width, y);
    }
    ctx.stroke();
  }

  function drawRoute(points) {
    ctx.beginPath();
    ctx.moveTo(points[0].x, points[0].y);
    for (let i = 1; i < points.length; i += 1) {
      ctx.lineTo(points[i].x, points[i].y);
    }
  }

  function drawBasePaths() {
    ctx.lineCap = "round";
    ctx.lineJoin = "round";

    for (const path of graph.paths) {
      drawRoute(path.points);
      ctx.strokeStyle = colors.lineSoft;
      ctx.lineWidth = 8;
      ctx.stroke();

      drawRoute(path.points);
      ctx.strokeStyle = colors.line;
      ctx.lineWidth = 1.5;
      ctx.stroke();
    }
  }

  function drawPulses(now) {
    ctx.globalCompositeOperation = "lighter";
    ctx.lineCap = "round";
    ctx.lineJoin = "round";

    for (const path of graph.paths) {
      const speed = 58 + path.phase * 46;
      const offset = -((now * 0.001 * speed + path.phase * 360) % 420);

      drawRoute(path.points);
      ctx.strokeStyle = path.tone;
      ctx.lineWidth = 2.4;
      ctx.setLineDash([72, 348]);
      ctx.lineDashOffset = offset;
      ctx.stroke();

      drawRoute(path.points);
      ctx.strokeStyle = path.tone.replace(/0\.\d+\)$/, "0.16)");
      ctx.lineWidth = 6;
      ctx.setLineDash([18, 402]);
      ctx.lineDashOffset = offset - 18;
      ctx.stroke();
    }

    ctx.setLineDash([]);
    ctx.globalCompositeOperation = "source-over";
  }

  function drawCards(now) {
    ctx.font = "600 11px Inter, system-ui, sans-serif";
    ctx.textBaseline = "middle";

    for (const item of graph.cards) {
      const pulse = 0.12 + Math.sin(now * 0.0016 + item.center.x * 0.03) * 0.04;

      ctx.fillStyle = colors.card;
      ctx.strokeStyle = colors.cardLine;
      ctx.lineWidth = 1;
      roundRect(item.x, item.y, item.w, item.h, 7);
      ctx.fill();
      ctx.stroke();

      ctx.strokeStyle = "rgba(244, 183, 64, " + pulse.toFixed(3) + ")";
      roundRect(item.x - 3, item.y - 3, item.w + 6, item.h + 6, 9);
      ctx.stroke();

      ctx.fillStyle = colors.text;
      ctx.fillText(item.label, item.x + 14, item.y + item.h / 2);
    }
  }

  function drawJunctions(now) {
    for (const path of graph.paths) {
      for (const point of path.points) {
        const alpha = 0.16 + Math.sin(now * 0.0018 + point.x * 0.01) * 0.05;
        ctx.fillStyle = "rgba(247, 243, 232, " + alpha.toFixed(3) + ")";
        ctx.beginPath();
        ctx.arc(point.x, point.y, 2.4, 0, Math.PI * 2);
        ctx.fill();
      }
    }
  }

  function roundRect(x, y, w, h, r) {
    const radius = Math.min(r, w / 2, h / 2);
    ctx.beginPath();
    ctx.moveTo(x + radius, y);
    ctx.lineTo(x + w - radius, y);
    ctx.quadraticCurveTo(x + w, y, x + w, y + radius);
    ctx.lineTo(x + w, y + h - radius);
    ctx.quadraticCurveTo(x + w, y + h, x + w - radius, y + h);
    ctx.lineTo(x + radius, y + h);
    ctx.quadraticCurveTo(x, y + h, x, y + h - radius);
    ctx.lineTo(x, y + radius);
    ctx.quadraticCurveTo(x, y, x + radius, y);
  }

  function drawFrame(now) {
    if (!graph) {
      return;
    }
    drawBackground();
    drawBasePaths();
    drawPulses(now);
    drawJunctions(now);
    drawCards(now);
  }

  function step(now) {
    drawFrame(now);
    if (!reducedMotion.matches) {
      raf = requestAnimationFrame(step);
    }
  }

  function start() {
    cancelAnimationFrame(raf);
    raf = requestAnimationFrame(step);
  }

  window.addEventListener("resize", function () {
    resize();
    if (!reducedMotion.matches) {
      start();
    }
  }, { passive: true });

  reducedMotion.addEventListener("change", function () {
    if (reducedMotion.matches) {
      cancelAnimationFrame(raf);
      drawFrame(performance.now());
    } else {
      start();
    }
  });

  resize();
  if (!reducedMotion.matches) {
    start();
  }
})();
