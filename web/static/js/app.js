// Confirmation before submitting forms marked with data-confirm, using the
// HTML <dialog> of the layout instead of the browser's window.confirm().
// With data-confirm-if (a selector), only when the form has a match, such as
// a ticked "also send an e-mail" box.
// The dialog is styled like the rest of the page (themes, language) and turns
// red when the form contains a destructive (.btn-danger) button.
(() => {
  const dialog = document.getElementById("confirm-dialog");
  if (!dialog) return;

  const message = dialog.querySelector("[data-confirm-message]");
  const ok = dialog.querySelector("[data-confirm-ok]");

  // What to do once confirmed.
  let pending = null;

  const needsConfirm = (form) =>
    form &&
    form.dataset &&
    form.dataset.confirm &&
    form.dataset.confirmed !== "yes" &&
    !(form.dataset.confirmIf && !form.querySelector(form.dataset.confirmIf));

  const ask = (form, proceed) => {
    pending = proceed;
    const danger = form.querySelector(".btn-danger") !== null;
    message.textContent = form.dataset.confirm;
    ok.className = danger ? "btn-danger-solid" : "btn";
    dialog.showModal();
  };

  document.addEventListener("submit", (event) => {
    const form = event.target;
    // htmx sends its forms itself: they are asked on htmx:confirm.
    if (!needsConfirm(form) || form.hasAttribute("hx-post")) return;

    event.preventDefault();
    const submitter = event.submitter;

    // Submit again, keeping the button that was clicked, without asking a
    // second time.
    ask(form, () => {
      form.dataset.confirmed = "yes";
      if (submitter) {
        form.requestSubmit(submitter);
      } else {
        form.requestSubmit();
      }
      delete form.dataset.confirmed;
    });
  });

  document.addEventListener("htmx:confirm", (event) => {
    const form = event.detail.elt;
    if (!needsConfirm(form)) return;

    event.preventDefault();
    ask(form, () => event.detail.issueRequest(true));
  });

  dialog.addEventListener("close", () => {
    // The close event is asynchronous: ignore it if the dialog was already
    // reopened for another form.
    if (dialog.open) return;

    const proceed = pending;
    pending = null;

    if (proceed && dialog.returnValue === "ok") proceed();
  });

  // A click on the backdrop cancels.
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog) dialog.close("cancel");
  });
})();

// File upload dialogs (see the "upload" template): an "Add" button opens the
// dialog, the drop zone accepts a dragged file or opens the file picker on
// click or Enter/Space, the chosen file is checked (type, size) and previewed,
// and "Save" submits the form. The server validates the file again.
(() => {
  const formatSize = (dialog, n) =>
    n >= 1 << 20
      ? `${(n / (1 << 20)).toFixed(1)} ${dialog.dataset.unitMb}`
      : `${Math.max(1, Math.round(n / 1024))} ${dialog.dataset.unitKb}`;

  // accepts checks a file against an accept attribute (extensions and MIME
  // types).
  const accepts = (accept, file) => {
    if (!accept) return true;
    const name = file.name.toLowerCase();
    return accept.split(",").some((rule) => {
      rule = rule.trim().toLowerCase();
      if (rule.startsWith(".")) return name.endsWith(rule);
      if (rule.endsWith("/*")) return file.type.startsWith(rule.slice(0, -1));
      return file.type === rule;
    });
  };

  document.querySelectorAll("dialog[data-upload]").forEach((dialog) => {
    const form = dialog.querySelector("form");
    const input = dialog.querySelector("[data-upload-input]");
    const zone = dialog.querySelector("[data-upload-zone]");
    const preview = dialog.querySelector("[data-upload-preview]");
    const placeholder = dialog.querySelector("[data-upload-placeholder]");
    const thumb = dialog.querySelector("[data-upload-thumb]");
    const pdf = dialog.querySelector("[data-upload-pdf]");
    const pages = dialog.querySelector("[data-upload-pages]");
    const icon = dialog.querySelector("[data-upload-icon]");
    // Incremented on each choice, so a slow render of a previous file is
    // dropped.
    let renderToken = 0;
    const name = dialog.querySelector("[data-upload-name]");
    const size = dialog.querySelector("[data-upload-size]");
    const error = dialog.querySelector("[data-upload-error]");
    const save = dialog.querySelector("[data-upload-save]");
    let thumbURL = null;

    const showError = (text) => {
      error.textContent = text;
      error.classList.toggle("hidden", !text);
    };

    // showPreview swaps the drop zone placeholder and the file preview.
    const showPreview = (visible) => {
      preview.classList.toggle("hidden", !visible);
      preview.classList.toggle("flex", visible);
      placeholder.classList.toggle("hidden", visible);
      placeholder.classList.toggle("flex", !visible);
    };

    // showPDF swaps the generic file icon and the rendered first page.
    const showPDF = (visible) => {
      if (!pdf) return;
      pdf.classList.toggle("hidden", !visible);
      pages.classList.toggle("hidden", !visible);
      if (icon) icon.classList.toggle("hidden", visible);
    };

    // renderPDF draws the first page with pdf.js, loaded on first use. On
    // failure the file icon stays.
    const renderPDF = async (file) => {
      const token = ++renderToken;
      showPDF(false);

      try {
        const pdfjs = await import(dialog.dataset.pdfjs);
        pdfjs.GlobalWorkerOptions.workerSrc = dialog.dataset.pdfjsWorker;

        // The loading task owns the document and its worker resources.
        const task = pdfjs.getDocument({ data: new Uint8Array(await file.arrayBuffer()) });
        try {
          const doc = await task.promise;
          const page = await doc.getPage(1);
          const natural = page.getViewport({ scale: 1 });
          // Fit the page in 200×280 CSS pixels, sharp on high-density screens.
          const scale = Math.min(200 / natural.width, 280 / natural.height);
          const ratio = window.devicePixelRatio || 1;
          const viewport = page.getViewport({ scale: scale * ratio });

          const canvas = document.createElement("canvas");
          canvas.width = Math.floor(viewport.width);
          canvas.height = Math.floor(viewport.height);
          await page.render({ canvas, viewport }).promise;

          if (token !== renderToken) return;
          pdf.width = canvas.width;
          pdf.height = canvas.height;
          pdf.style.width = `${Math.round(viewport.width / ratio)}px`;
          pdf.style.height = `${Math.round(viewport.height / ratio)}px`;
          pdf.getContext("2d").drawImage(canvas, 0, 0);
          pages.textContent = dialog.dataset.msgPages.replace("%d", doc.numPages);
          showPDF(true);
        } finally {
          task.destroy();
        }
      } catch (err) {
        console.warn(`PDF preview unavailable: ${err && err.message ? err.message : err}`);
      }
    };

    const reset = () => {
      renderToken++;
      form.reset();
      save.disabled = true;
      showPreview(false);
      showError("");
      name.textContent = "";
      size.textContent = "";
      showPDF(false);
      if (thumbURL) URL.revokeObjectURL(thumbURL);
      thumbURL = null;
    };

    const choose = (file) => {
      showError("");
      save.disabled = true;
      showPreview(false);
      if (!file) return;

      if (!accepts(dialog.dataset.accept, file)) {
        showError(dialog.dataset.msgBadType);
        return;
      }
      if (file.size > Number(dialog.dataset.maxBytes)) {
        showError(dialog.dataset.msgTooLarge);
        return;
      }

      name.textContent = file.name;
      size.textContent = formatSize(dialog, file.size);
      if (thumbURL) URL.revokeObjectURL(thumbURL);
      thumbURL = URL.createObjectURL(file);
      if (thumb) thumb.src = thumbURL;

      if (pdf && file.type === "application/pdf") renderPDF(file);
      showPreview(true);
      save.disabled = false;
    };

    document.querySelectorAll(`[data-upload-open="${dialog.id}"]`).forEach((button) =>
      button.addEventListener("click", () => {
        reset();
        dialog.showModal();
        zone.focus();
      }),
    );

    // A target outside the dialog (the picture shown on the page) is a drop
    // zone too: a click opens the file picker straight away, a dropped file
    // opens the dialog with it, ready to save.
    let fromTarget = false;

    const load = (file) => {
      const transfer = new DataTransfer();
      transfer.items.add(file);
      input.files = transfer.files;
      choose(file);
    };

    document.querySelectorAll(`[data-upload-target="${dialog.id}"]`).forEach((target) => {
      const pick = () => {
        reset();
        fromTarget = true;
        input.click();
      };

      target.addEventListener("click", pick);
      target.addEventListener("keydown", (event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          pick();
        }
      });
      target.addEventListener("dragover", (event) => {
        event.preventDefault();
        target.dataset.dragging = "";
      });
      target.addEventListener("dragleave", () => delete target.dataset.dragging);
      target.addEventListener("drop", (event) => {
        event.preventDefault();
        delete target.dataset.dragging;
        const file = event.dataTransfer.files[0];
        if (!file) return;
        reset();
        load(file);
        dialog.showModal();
      });
    });

    zone.addEventListener("click", () => input.click());
    dialog.querySelector("[data-upload-browse]").addEventListener("click", () => input.click());
    zone.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        input.click();
      }
    });

    input.addEventListener("change", () => {
      choose(input.files[0]);
      // Picked from the page: the dialog shows the choice and saves it.
      if (fromTarget && input.files[0] && !dialog.open) dialog.showModal();
      fromTarget = false;
    });

    zone.addEventListener("dragover", (event) => {
      event.preventDefault();
      zone.dataset.dragging = "";
    });
    zone.addEventListener("dragleave", () => delete zone.dataset.dragging);
    zone.addEventListener("drop", (event) => {
      event.preventDefault();
      delete zone.dataset.dragging;
      const file = event.dataTransfer.files[0];
      if (!file) return;
      // Put the dropped file in the form's file field.
      load(file);
    });

    form.addEventListener("submit", () => {
      save.disabled = true;
    });

    // A file dropped next to the zone must not make the browser open it.
    dialog.addEventListener("dragover", (event) => event.preventDefault());
    dialog.addEventListener("drop", (event) => event.preventDefault());

    dialog.querySelector("[data-upload-cancel]").addEventListener("click", () => dialog.close());
    dialog.addEventListener("click", (event) => {
      if (event.target === dialog) dialog.close();
    });
    // The close event is dispatched asynchronously: if the dialog was
    // reopened in the meantime, resetting would discard the new choice.
    dialog.addEventListener("close", () => {
      if (!dialog.open) reset();
    });
  });
})();

// Drop-down menus (<details data-menu>): close on outside click and Escape,
// and keep a single one open.
(() => {
  const menus = () => document.querySelectorAll("details[data-menu][open]");

  document.addEventListener("click", (event) => {
    menus().forEach((menu) => {
      if (!menu.contains(event.target)) menu.removeAttribute("open");
    });
  });

  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") return;
    menus().forEach((menu) => {
      menu.removeAttribute("open");
      menu.querySelector("summary").focus();
    });
  });
})();

// Volunteer space helpers, wired with data attributes since the content
// security policy forbids inline scripts.
(() => {
  // A checkbox that submits its form when ticked (slot task lists).
  document.addEventListener("change", (event) => {
    const box = event.target;
    if (box.matches && box.matches("[data-autosubmit]")) box.form.requestSubmit();
  });

  // A read-only field selected as a whole on focus (subscription links).
  document.addEventListener("focusin", (event) => {
    if (event.target.matches && event.target.matches("[data-select-on-focus]")) event.target.select();
  });

  // A button printing the page (printed rosters), which the browser can
  // also save as a PDF.
  document.addEventListener("click", (event) => {
    if (event.target.closest && event.target.closest("[data-print]")) window.print();
  });

  // A "Copy" button copying the value of the field data-copy points at.
  document.addEventListener("click", (event) => {
    const btn = event.target.closest && event.target.closest("[data-copy]");
    if (!btn) return;

    const field = document.querySelector(btn.dataset.copy);
    if (!field) return;

    const label = btn.textContent;
    const done = (text) => {
      btn.textContent = text;
      setTimeout(() => (btn.textContent = label), 2500);
    };

    // The Clipboard API needs a secure context: select the text otherwise.
    const fallback = () => {
      field.select();
      done("Ctrl/⌘ + C");
    };

    if (!navigator.clipboard || !navigator.clipboard.writeText) return fallback();
    navigator.clipboard.writeText(field.value).then(() => done(btn.dataset.copied || "✓"), fallback);
  });

  // After htmx replaces a slot card, keyboard focus would fall back to the
  // page: put it on the matching control of the new card.
  document.addEventListener("htmx:afterSwap", (event) => {
    const target = event.detail && event.detail.target;
    if (!target || (document.activeElement && document.activeElement !== document.body)) return;

    const card = (target.id && document.getElementById(target.id)) || target;
    const next = card.querySelector && card.querySelector("[data-autofocus], button");
    if (next) next.focus({ preventScroll: true });
  });
})();

// An htmx request that fails leaves the page as it was: the button comes
// back and a ticked box stays ticked, as if it had worked. Say so, offline
// (venue wifi) or refused, and untick what was not saved.
(() => {
  const box = document.getElementById("request-error");
  if (!box) return;

  let timer = null;

  const fail = (event, text) => {
    box.textContent = text;
    box.classList.remove("hidden");
    clearTimeout(timer);
    timer = setTimeout(() => box.classList.add("hidden"), 8000);

    const elt = event.detail && event.detail.elt;
    const form = elt && (elt.closest ? elt.closest("form") : null);
    if (form) {
      form.querySelectorAll("input[data-autosubmit]").forEach((input) => (input.checked = !input.checked));
    }
  };

  document.addEventListener("htmx:sendError", (event) => fail(event, box.dataset.offline));
  document.addEventListener("htmx:responseError", (event) => fail(event, box.dataset.failed));
  document.addEventListener("htmx:afterRequest", (event) => {
    if (event.detail && event.detail.successful) box.classList.add("hidden");
  });
})();

// The slot generator, hours per day: one block per day of the dates chosen,
// following them as they change. A day kept keeps what was typed for it; a
// day added (a set-up day before the event, say) comes from the template,
// with the common hours. The server reads the blocks of the days posted.
(() => {
  const box = document.querySelector("[data-gen-days]");
  if (!box) return;

  const form = box.closest("form");
  const from = form.querySelector("[name=from]");
  const to = form.querySelector("[name=to]");
  const template = box.querySelector("[data-gen-day-template]");
  const none = box.querySelector("[data-gen-no-days]");
  const label = new Intl.DateTimeFormat(box.dataset.lang, { weekday: "long", day: "numeric", month: "long", timeZone: "UTC" });

  // days lists the dates from..to as YYYY-MM-DD, none when they make no
  // sense or span more than the server takes.
  const days = () => {
    const a = new Date(from.value + "T00:00:00Z");
    const b = new Date(to.value + "T00:00:00Z");
    if (isNaN(a) || isNaN(b) || b < a || (b - a) / 86400000 > 31) return [];

    const out = [];
    for (let d = a; d <= b; d = new Date(d.getTime() + 86400000)) out.push(d.toISOString().slice(0, 10));
    return out;
  };

  // commonHours copies the common hours into the fields of a new day.
  const commonHours = (block, day) => {
    const prefix = `d${day}.`;
    for (const name of ["day_start", "day_end", "length", "list"]) {
      const src = form.querySelector(`[name="${name}"]`);
      const dst = block.querySelector(`[name="${prefix}${name}"]`);
      if (src && dst) dst.value = src.value;
    }

    const mode = form.querySelector('[name="mode"]:checked');
    const radio = mode && block.querySelector(`[name="${prefix}mode"][value="${mode.value}"]`);
    if (radio) radio.checked = true;
  };

  const sync = () => {
    const want = days();
    const have = new Map([...box.querySelectorAll("[data-gen-day]")].filter((el) => !el.closest("template")).map((el) => [el.dataset.genDay, el]));

    for (const [day, el] of have) if (!want.includes(day)) el.remove();

    let prev = none;
    for (const day of want.slice().reverse()) {
      let el = have.get(day);
      if (!el) {
        const html = template.innerHTML.replaceAll("__DATE__", day);
        const holder = document.createElement("div");
        holder.innerHTML = html.trim();
        el = holder.firstElementChild;
        el.querySelector("h3").textContent = label.format(new Date(day + "T00:00:00Z"));
        commonHours(el, day);
      }
      box.insertBefore(el, prev);
      prev = el;
    }

    none.classList.toggle("hidden", want.length > 0);
  };

  from.addEventListener("change", sync);
  to.addEventListener("change", sync);
})();

// The generator's preview: the totals and the "Create" button follow the
// cells ticked and the volunteers typed, before the page is sent.
(() => {
  const preview = document.querySelector("[data-gen-preview]");
  if (!preview) return;

  const total = preview.querySelector("[data-gen-total]");
  const create = document.querySelector("[data-gen-create]");
  const en = preview.dataset.lang === "en";
  // French counts 0 and 1 as singular, English only 1.
  const pick = (el, n, one, other) => ((n === 1 || (n === 0 && !en)) ? el.dataset[one] : el.dataset[other]);

  const update = () => {
    let slots = 0;
    let seats = 0;
    preview.querySelectorAll("[data-gen-cell]").forEach((box) => {
      if (!box.checked) return;
      slots++;
      seats += Math.max(0, parseInt(box.parentElement.querySelector("[data-gen-cap]").value, 10) || 0);
    });

    total.textContent = pick(preview, slots, "totalOne", "totalOther").replace("%d", slots).replace("%d", seats);
    if (create) {
      create.textContent = pick(create, slots, "one", "other").replace("%d", slots);
      create.disabled = slots === 0;
    }
  };

  preview.addEventListener("change", update);
  preview.addEventListener("input", update);
})();

// The slots of a pole in bulk: the delete button and its confirmation say
// how many slots are ticked and how many sign-ups go with them; a day's box
// ticks all of its slots.
(() => {
  const form = document.querySelector("[data-bulk]");
  if (!form) return;

  const button = form.querySelector("[data-bulk-delete]");
  const en = form.dataset.lang === "en";
  const pick = (n, one, other) => (n === 1 || (n === 0 && !en) ? form.dataset[one] : form.dataset[other]);
  // format fills "%d" in turn, and "%[2]d" by position.
  const format = (text, ...args) => {
    let i = 0;
    return text.replace(/%\[(\d)\]d/g, (_, n) => args[n - 1]).replace(/%d/g, () => args[i++]);
  };

  const update = () => {
    const boxes = [...document.querySelectorAll('input[form="bulk-delete"][name=slot]:checked')];
    const signups = boxes.reduce((n, box) => n + (parseInt(box.dataset.filled, 10) || 0), 0);

    button.textContent = format(pick(boxes.length, "one", "other"), boxes.length);
    button.disabled = boxes.length === 0;
    form.dataset.confirm = format(pick(boxes.length, "confirmOne", "confirmOther"), boxes.length, signups);
  };

  document.addEventListener("change", (event) => {
    const all = event.target.closest && event.target.closest("[data-bulk-all]");
    if (all) {
      all.closest("section").querySelectorAll('input[name=slot]').forEach((box) => (box.checked = all.checked));
    }

    if (all || (event.target.matches && event.target.matches('input[form="bulk-delete"]'))) update();
  });

  update();
})();

// A search field filtering the rows of a table (data-filter: the table),
// with a live count (data-filter-count, data-one / data-other with %d).
(() => {
  document.addEventListener("input", (event) => {
    const input = event.target;
    if (!input.matches || !input.matches("[data-filter]")) return;

    const needle = input.value.toLowerCase();
    let shown = 0;

    document.querySelectorAll(`${input.dataset.filter} tbody tr`).forEach((row) => {
      const match = row.textContent.toLowerCase().includes(needle);
      row.hidden = !match;
      if (match) shown++;
    });

    const count = document.querySelector(input.dataset.filterCount || "");
    if (count) count.textContent = (shown === 1 ? input.dataset.one : input.dataset.other).replace("%d", shown);
  });
})();
