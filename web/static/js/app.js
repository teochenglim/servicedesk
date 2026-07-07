// initMarkdownEditors mounts a Toast UI Editor (DESIGN/08 §8.7/§8.11: every
// text surface gets Markdown with a raw/rendered toggle - Toast UI's built-in
// WYSIWYG<->Markdown mode switch is that toggle) onto every
// ".md-editor-mount" under root that hasn't been initialized yet, syncing its
// content into the paired hidden textarea just before the enclosing form
// submits. Called on both initial page load and every htmx swap (mirroring
// hljs.highlightAll's dual-hook pattern below), since ticket-detail swaps can
// inject a fresh note composer without a full page reload.
function initMarkdownEditors(root) {
  root.querySelectorAll(".md-editor-mount:not([data-md-initialized])").forEach(function (mount) {
    mount.dataset.mdInitialized = "1";
    var hidden = document.getElementById(mount.dataset.hiddenId);
    if (!hidden) return;
    var editor = new toastui.Editor({
      el: mount,
      height: mount.dataset.height || "220px",
      initialEditType: "wysiwyg",
      previewStyle: "tab",
      initialValue: hidden.value || "",
      usageStatistics: false,
    });
    var form = mount.closest("form");
    if (form) {
      form.addEventListener("submit", function () {
        hidden.value = editor.getMarkdown();
      });
    }
  });
}

document.addEventListener("DOMContentLoaded", function () {
  hljs.highlightAll();
  initMarkdownEditors(document);
  document.body.addEventListener("htmx:afterSwap", function () {
    hljs.highlightAll();
    initMarkdownEditors(document);
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
