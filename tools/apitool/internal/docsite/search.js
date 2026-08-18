(function () {
  "use strict";

  // "Show child attributes/parameters" toggles: each button reveals the
  // sibling .attr-children block it names via aria-controls. Works for any
  // number of buttons at any nesting depth - a nested toggle inside an
  // already-expanded block behaves identically, since each just owns its
  // own target and label.
  document.querySelectorAll(".expand-toggle").forEach(function (btn) {
    var target = document.getElementById(btn.getAttribute("aria-controls"));
    var sign = btn.querySelector(".sign");
    btn.addEventListener("click", function () {
      var open = btn.getAttribute("aria-expanded") === "true";
      btn.setAttribute("aria-expanded", String(!open));
      target.hidden = open;
      sign.textContent = open ? "+" : "−";
      btn.lastChild.textContent = " " + (open ? "Show fields" : "Hide fields");
    });
  });

  var index = JSON.parse(document.getElementById("apitool-index").textContent);
  var input = document.getElementById("search-input");
  var results = document.getElementById("search-results");

  var KIND_LABELS = {
    "resource": "Resources",
    "method": "Methods",
    "message": "Messages",
    "field": "Fields",
    "enum": "Enums",
    "enum value": "Enum values"
  };
  var KIND_ORDER = ["resource", "method", "message", "field", "enum", "enum value"];
  var MAX_RESULTS = 40;

  var activeIndex = -1;
  var visible = [];

  function matches(entry, query) {
    return entry.name.toLowerCase().indexOf(query) !== -1 ||
      entry.fullName.toLowerCase().indexOf(query) !== -1;
  }

  function search(query) {
    query = query.trim().toLowerCase();
    if (!query) {
      return [];
    }
    var out = [];
    for (var i = 0; i < index.length && out.length < MAX_RESULTS; i++) {
      if (matches(index[i], query)) {
        out.push(index[i]);
      }
    }
    return out;
  }

  function render(entries) {
    results.innerHTML = "";
    visible = entries;
    activeIndex = -1;

    if (entries.length === 0) {
      var empty = document.createElement("div");
      empty.className = "empty";
      empty.textContent = "No matches";
      results.appendChild(empty);
      results.hidden = false;
      return;
    }

    var byKind = {};
    entries.forEach(function (e) {
      (byKind[e.kind] = byKind[e.kind] || []).push(e);
    });

    KIND_ORDER.forEach(function (kind) {
      var group = byKind[kind];
      if (!group || group.length === 0) {
        return;
      }
      var label = document.createElement("div");
      label.className = "group-label";
      label.textContent = KIND_LABELS[kind] || kind;
      results.appendChild(label);

      group.forEach(function (entry) {
        var row = document.createElement("div");
        row.className = "result";
        row.dataset.anchor = entry.anchor;

        var name = document.createElement("span");
        name.className = "mono";
        name.textContent = entry.name;
        row.appendChild(name);

        var full = document.createElement("span");
        full.className = "full-name mono";
        full.textContent = entry.fullName;
        row.appendChild(full);

        row.addEventListener("mousedown", function (ev) {
          // mousedown, not click: fires before the input's blur handler
          // would otherwise hide the dropdown first.
          ev.preventDefault();
          navigate(entry.anchor);
        });

        results.appendChild(row);
      });
    });

    results.hidden = false;
  }

  function navigate(anchor) {
    results.hidden = true;
    var el = document.getElementById(anchor);
    if (!el) {
      return;
    }
    if (window.location.hash === "#" + anchor) {
      el.scrollIntoView({ block: "start" });
    } else {
      window.location.hash = anchor;
    }
  }

  function setActive(i) {
    var rows = results.querySelectorAll(".result");
    if (activeIndex >= 0 && rows[activeIndex]) {
      rows[activeIndex].classList.remove("active");
    }
    activeIndex = i;
    if (activeIndex >= 0 && rows[activeIndex]) {
      rows[activeIndex].classList.add("active");
      rows[activeIndex].scrollIntoView({ block: "nearest" });
    }
  }

  input.addEventListener("input", function () {
    render(search(input.value));
  });

  input.addEventListener("keydown", function (ev) {
    var rows = results.querySelectorAll(".result");
    if (ev.key === "ArrowDown") {
      ev.preventDefault();
      if (rows.length > 0) {
        setActive((activeIndex + 1) % rows.length);
      }
    } else if (ev.key === "ArrowUp") {
      ev.preventDefault();
      if (rows.length > 0) {
        setActive((activeIndex - 1 + rows.length) % rows.length);
      }
    } else if (ev.key === "Enter") {
      if (activeIndex >= 0 && visible[activeIndex]) {
        navigate(visible[activeIndex].anchor);
      } else if (visible.length > 0) {
        navigate(visible[0].anchor);
      }
    } else if (ev.key === "Escape") {
      results.hidden = true;
      input.blur();
    }
  });

  input.addEventListener("focus", function () {
    if (input.value.trim() && visible.length > 0) {
      results.hidden = false;
    }
  });

  document.addEventListener("click", function (ev) {
    if (ev.target !== input && !results.contains(ev.target)) {
      results.hidden = true;
    }
  });
})();
