document.addEventListener("DOMContentLoaded", function () {
  hljs.highlightAll();
  document.body.addEventListener("htmx:afterSwap", function () {
    hljs.highlightAll();
  });

  document.body.addEventListener("htmx:beforeRequest", function (evt) {
    var row = evt.target.closest(".ticket-row");
    if (!row) return;
    document.querySelectorAll(".ticket-row.selected").forEach(function (el) { el.classList.remove("selected"); });
    row.classList.add("selected");
  });

  if (!window.EventSource) return;

  var es = new EventSource("/events");
  es.addEventListener("message", function () {});
  ["ticket.status_changed", "ticket.assigned", "ticket.updated", "ticket.label_added",
   "note.added.external", "note.added.internal", "workflow.notify"].forEach(function (evt) {
    es.addEventListener(evt, function (e) {
      var data = JSON.parse(e.data);
      var pane = document.getElementById("ticket-detail-pane");
      var ticketId = pane && pane.dataset.ticketId;
      if (ticketId && String(data.ticket_id) === ticketId) {
        htmx.ajax("GET", window.location.pathname + " #ticket-detail-pane", { target: "#ticket-detail-pane", swap: "outerHTML" });
      }
      var badge = document.getElementById("live-badge");
      if (badge) {
        badge.classList.add("flash");
        setTimeout(function () { badge.classList.remove("flash"); }, 800);
      }
    });
  });
});
