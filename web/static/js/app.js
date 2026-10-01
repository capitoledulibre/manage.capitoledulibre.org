// Confirmation before submitting forms marked with data-confirm, using the
// HTML <dialog> of the layout instead of the browser's window.confirm().
// The dialog is styled like the rest of the page (themes, language) and turns
// red when the form contains a destructive (.btn-danger) button.
(() => {
  const dialog = document.getElementById("confirm-dialog");
  if (!dialog) return;

  const message = dialog.querySelector("[data-confirm-message]");
  const ok = dialog.querySelector("[data-confirm-ok]");

  let pending = null;

  document.addEventListener("submit", (event) => {
    const form = event.target;
    if (!form.dataset.confirm || form.dataset.confirmed === "yes") return;

    event.preventDefault();
    pending = { form, submitter: event.submitter };

    const danger = form.querySelector(".btn-danger") !== null;
    message.textContent = form.dataset.confirm;
    ok.className = danger ? "btn-danger-solid" : "btn";
    dialog.showModal();
  });

  dialog.addEventListener("close", () => {
    // The close event is asynchronous: ignore it if the dialog was already
    // reopened for another form.
    if (dialog.open) return;

    const current = pending;
    pending = null;

    if (!current || dialog.returnValue !== "ok") return;

    // Submit again, keeping the button that was clicked, without asking a
    // second time.
    current.form.dataset.confirmed = "yes";
    if (current.submitter) {
      current.form.requestSubmit(current.submitter);
    } else {
      current.form.requestSubmit();
    }
    delete current.form.dataset.confirmed;
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

    zone.addEventListener("click", () => input.click());
    dialog.querySelector("[data-upload-browse]").addEventListener("click", () => input.click());
    zone.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        input.click();
      }
    });

    input.addEventListener("change", () => choose(input.files[0]));

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
      const transfer = new DataTransfer();
      transfer.items.add(file);
      input.files = transfer.files;
      choose(file);
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
