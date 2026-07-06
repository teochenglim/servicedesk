document.addEventListener("DOMContentLoaded", function () {
  hljs.highlightAll();
  document.body.addEventListener("htmx:afterSwap", function () {
    hljs.highlightAll();
  });

  var ticketId = document.body.dataset.ticketId;
  if (!window.EventSource) return;

  var es = new EventSource("/events");
  es.addEventListener("message", function () {});
  ["ticket.status_changed", "ticket.assigned", "ticket.updated", "ticket.label_added",
   "note.added.external", "note.added.internal", "workflow.notify"].forEach(function (evt) {
    es.addEventListener(evt, function (e) {
      var data = JSON.parse(e.data);
      if (ticketId && String(data.ticket_id) === ticketId) {
        htmx.ajax("GET", window.location.pathname + " #ticket-panel", { target: "#ticket-panel", swap: "outerHTML" });
      }
      var badge = document.getElementById("live-badge");
      if (badge) {
        badge.classList.add("flash");
        setTimeout(function () { badge.classList.remove("flash"); }, 800);
      }
    });
  });
});
