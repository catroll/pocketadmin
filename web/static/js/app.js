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
      renderTimeSeries();
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

    /**
     * Draws every host-metric chart on the page.
     *
     * Each `.chart-box[data-series]` becomes one line chart. The payload is the shape
     * the server builds: labels, one value per label, and a unit. A null value is a
     * bucket with no reading, which Chart.js draws as a break -- a machine that was
     * down for an hour must not look like a machine that was idle.
     */
    function renderTimeSeries() {
      var boxes = document.querySelectorAll('.chart-box[data-series]');
      if (!boxes.length || typeof Chart === 'undefined') {
        return;
      }

      Array.prototype.forEach.call(boxes, function (box) {
        var data;
        try {
          data = JSON.parse(box.dataset.series || '{}');
        } catch (err) {
          return;
        }

        if (!data || !Array.isArray(data.labels) || !data.labels.length) {
          return;
        }

        var canvas = document.createElement('canvas');
        box.appendChild(canvas);

        var options = {
          responsive: true,
          maintainAspectRatio: false,
          animation: false,
          interaction: { mode: 'index', intersect: false },
          plugins: {
            legend: { display: false },
            tooltip: {
              callbacks: {
                label: function (context) {
                  return formatValue(context.parsed.y, data);
                }
              }
            }
          },
          elements: {
            point: { radius: 0, hitRadius: 8 },
            line: { borderWidth: 2, tension: 0.25, spanGaps: false }
          },
          scales: {
            x: {
              ticks: { maxTicksLimit: 8, autoSkip: true, maxRotation: 0 },
              grid: { display: false }
            },
            y: {
              beginAtZero: true,
              ticks: {
                maxTicksLimit: 5,
                callback: function (value) {
                  return formatValue(value, data);
                }
              }
            }
          }
        };

        if (typeof data.max === 'number') {
          options.scales.y.max = data.max;
        }

        new Chart(canvas, {
          type: 'line',
          data: {
            labels: data.labels,
            datasets: [{
              data: data.values,
              borderColor: accentColour(),
              backgroundColor: accentFill(),
              fill: true
            }]
          },
          options: options
        });
      });
    }

    /** Renders a reading with its unit, in the units an operator reads. */
    function formatValue(value, data) {
      if (value === null || value === undefined) {
        return 'no reading';
      }
      if (data.unit === 'B/s') {
        return humanBytes(value) + '/s';
      }
      if (data.unit === '%') {
        return Number(value).toFixed(1) + '%';
      }
      return Number(value).toFixed(2);
    }

    /** Renders a byte count. */
    function humanBytes(value) {
      var units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
      var n = Math.abs(Number(value));
      var i = 0;
      while (n >= 1024 && i < units.length - 1) {
        n = n / 1024;
        i++;
      }
      return (value < 0 ? '-' : '') + n.toFixed(n < 10 && i > 0 ? 1 : 0) + ' ' + units[i];
    }

    /** The theme's accent colour, read from the document rather than hard-coded. */
    function accentColour() {
      var value = getComputedStyle(document.documentElement)
        .getPropertyValue('--bs-primary').trim();
      return value || '#0d6efd';
    }

    /** A translucent fill of the same colour. */
    function accentFill() {
      var colour = accentColour();
      if (colour.charAt(0) !== '#') {
        return 'rgba(13, 110, 253, 0.12)';
      }
      var hex = colour.substring(1);
      if (hex.length === 3) {
        hex = hex.charAt(0) + hex.charAt(0) + hex.charAt(1) + hex.charAt(1) + hex.charAt(2) + hex.charAt(2);
      }
      var r = parseInt(hex.substring(0, 2), 16);
      var g = parseInt(hex.substring(2, 4), 16);
      var b = parseInt(hex.substring(4, 6), 16);
      return 'rgba(' + r + ', ' + g + ', ' + b + ', 0.12)';
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
