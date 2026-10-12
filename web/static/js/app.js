/*
 * PocketAdmin front-end glue.
 *
 * Everything here is progressive enhancement: the panel is fully usable with
 * JavaScript disabled (forms are plain HTML posts).
 *
 * No jQuery. Chart.js 4 and Bootstrap 5 both ship ES5-compatible UMD builds.
 */
(function () {
  'use strict';

  document.addEventListener('DOMContentLoaded', function () {
    renderRecordsChart();
    autoSubmitFilters();
  });

  /**
   * Draws the "records per collection" bar chart from the data-chart attribute
   * emitted by the dashboard template.
   *
   * Chart.js 4 replaced the v2 `scales.yAxes[]` / `legend` / `tooltips` options
   * with `scales.y`, `plugins.legend` and `plugins.tooltip`.
   */
  function renderRecordsChart() {
    var canvas = document.getElementById('records-chart');
    if (!canvas || typeof Chart === 'undefined') {
      return;
    }

    var data;
    try {
      data = JSON.parse(canvas.dataset.chart || '{}');
    } catch (err) {
      return;
    }

    if (!data || !Array.isArray(data.Labels) || data.Labels.length === 0) {
      return;
    }

    // Chart.js 4 needs a canvas element; the template renders a sized container.
    var target = document.createElement('canvas');
    canvas.appendChild(target);

    new Chart(target, {
      type: 'bar',
      data: {
        labels: data.Labels,
        datasets: [{
          label: 'Records',
          backgroundColor: 'rgba(13, 110, 253, 0.75)',
          borderColor: 'rgba(13, 110, 253, 1)',
          borderWidth: 1,
          data: data.Values
        }]
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
          legend: { display: false },
          tooltip: { mode: 'index', intersect: false }
        },
        scales: {
          y: {
            beginAtZero: true,
            ticks: { precision: 0 }
          }
        }
      }
    });
  }

  /** Submits the record filter form when the page-size selector changes. */
  function autoSubmitFilters() {
    var select = document.getElementById('perPage');
    if (select && select.form) {
      select.addEventListener('change', function () {
        select.form.submit();
      });
    }
  }
})();
