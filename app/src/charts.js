/**
 * 图表封装：基于 Chart.js，重复渲染时销毁旧实例。
 */
(function (root) {
  const store = {};
  const arr = (v) => (Array.isArray(v) ? v : []);

  function mount(id, config) {
    const canvas = document.getElementById(id);
    if (!canvas || typeof root.Chart === 'undefined') return;
    if (store[id]) store[id].destroy();
    store[id] = new root.Chart(canvas.getContext('2d'), config);
  }

  const palette = ['#2f6feb', '#2e9e5b', '#e8a33d', '#8b5cf6', '#d64545', '#0ea5e9', '#a16207', '#64748b'];

  /** 类别占比环形图。 */
  function category(rawItems) {
    const items = arr(rawItems);
    mount('chart-category', {
      type: 'doughnut',
      data: {
        labels: items.map((i) => i.label),
        datasets: [{
          data: items.map((i) => i.amount),
          backgroundColor: items.map((_, idx) => palette[idx % palette.length]),
        }],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
          legend: { position: 'right' },
          tooltip: {
            callbacks: {
              label: (ctx) => ` ${ctx.label}: ${money(ctx.parsed)}（${pct(ctx.parsed, items.reduce((s, i) => s + i.amount, 0))}）`,
            },
          },
        },
      },
    });
  }

  /** 账户分布条形图。 */
  function accounts(rawItems) {
    const items = arr(rawItems);
    const ellipsis = (name) => (Array.from(name).length > 10 ? Array.from(name).slice(0, 10).join('') + '…' : name);
    mount('chart-account', {
      type: 'bar',
      data: {
        labels: items.map((i) => ellipsis(i.name)),
        datasets: [{
          label: '金额',
          data: items.map((i) => i.amount),
          // 负债（负值）用红色区分
          backgroundColor: items.map((i) => (i.amount < 0 ? '#d64545' : '#2f6feb')),
          maxBarThickness: 24,
          borderRadius: 3,
        }],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        indexAxis: 'y',
        layout: { padding: { right: 10 } },
        plugins: {
          legend: { display: false },
          tooltip: {
            callbacks: {
              // 悬停显示完整账户名（坐标轴上是截断后的名称）
              title: (ctx) => (items[ctx[0].dataIndex] ? items[ctx[0].dataIndex].name : ''),
              label: (ctx) => ' ' + money(ctx.parsed.x),
            },
          },
        },
        scales: {
          x: { beginAtZero: true, ticks: { callback: (v) => short(v), maxTicksLimit: 6 } },
          y: { ticks: { autoSkip: false, font: { size: 11 } } },
        },
      },
    });
  }

  /** 时间序列折线图。 */
  function line(rawPoints) {
    const points = arr(rawPoints);
    mount('chart-line', {
      type: 'line',
      data: {
        labels: points.map((p) => p.date),
        datasets: [{
          label: '总资产',
          data: points.map((p) => p.total),
          borderColor: '#2f6feb',
          backgroundColor: 'rgba(47,111,235,0.12)',
          fill: true,
          tension: 0.25,
          pointRadius: 0,
        }],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: { tooltip: { callbacks: { label: (ctx) => ' ' + money(ctx.parsed.y) } } },
        scales: { y: { ticks: { callback: (v) => short(v) } } },
      },
    });
  }

  function money(v) {
    return (v || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  }
  function pct(v, total) {
    if (!total) return '0%';
    return ((v / total) * 100).toFixed(1) + '%';
  }
  function short(v) {
    if (Math.abs(v) >= 1e8) return (v / 1e8).toFixed(1) + '亿';
    if (Math.abs(v) >= 1e4) return (v / 1e4).toFixed(1) + '万';
    return String(v);
  }

  /** 重新计算所有图表尺寸（页签从隐藏切回可见时必须调用）。 */
  function resizeAll() {
    Object.keys(store).forEach((id) => {
      const chart = store[id];
      if (chart && typeof chart.resize === 'function') chart.resize();
    });
  }

  root.FamCharts = { category, accounts, line, money, palette, resizeAll };
})(typeof window !== 'undefined' ? window : this);
