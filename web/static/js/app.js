/*
 * PocketAdmin front-end glue.
 *
 * Everything here is progressive enhancement: the panel is fully usable with
 * JavaScript disabled (forms are plain HTML posts).
 */
(function () {
  'use strict';

  // AdminLTE 3 styles its sidebar scroller through OverlayScrollbars.
  if (window.jQuery && jQuery.fn && typeof jQuery.fn.overlayScrollbars === 'function') {
    jQuery(function () {
      jQuery('.sidebar').overlayScrollbars({ scrollbars: { clickScrolling: true } });
    });
  }

  document.addEventListener('DOMContentLoaded', function () {
    renderRecordsChart();
    autoSubmitFilters();
  });

  /**
   * Draws the "records per collection" bar chart from the data-chart attribute
   * emitted by the dashboard template.
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

    new Chart(canvas, {
      type: 'bar',
      data: {
        labels: data.Labels,
        datasets: [{
          label: 'Records',
          backgroundColor: 'rgba(60, 141, 188, 0.8)',
          borderColor: 'rgba(60, 141, 188, 1)',
          borderWidth: 1,
          data: data.Values
        }]
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        legend: { display: false },
        tooltips: { mode: 'index', intersect: false },
        scales: {
          yAxes: [{
            ticks: { beginAtZero: true, precision: 0 }
          }]
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
