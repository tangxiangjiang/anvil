// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Requirement version detail: inline editing, version-card drag/swap.
 *
 * Part of the Delivery feature object: assigns onto `window.Features.delivery`.
 */
window.Features = window.Features || {};
window.Features.delivery = window.Features.delivery || {};

Object.assign(window.Features.delivery, {
  isAwaitingStatus(st) {
    return String(st || "").toLowerCase().startsWith("awaiting_");
  },

  milestoneVersions(eng, major, minor) {
    return (eng.versions || [])
      .filter((v) => v.major === major && v.minor === minor)
      .slice()
      .sort((a, b) => a.patch - b.patch);
  },

  ensureMsVerDraft(editKey, versions) {
    if (this._msVerDraftKey === editKey && Array.isArray(this._msVerDraft)) return;
    this._msVerDraftKey = editKey;
    this._msVerDraft = versions.map((v) => ({
      patch: v.patch,
      title: v.title || "",
      problem: v.problem || "",
    }));
  },

  captureMsVerCardEdits(root) {
    if (!this._msVerDraft) return;
    root.querySelectorAll(".del-ms-ver-card").forEach((card) => {
      const patch = Number(card.getAttribute("data-patch"));
      const d = this._msVerDraft.find((x) => x.patch === patch);
      if (!d) return;
      const titleEl = card.querySelector(".del-ms-ver-edit-title");
      const noteEl = card.querySelector(".del-ms-ver-edit-note");
      if (titleEl) d.title = titleEl.value;
      if (noteEl) d.problem = noteEl.value;
    });
  },

  cancelMsVerCardEdit() {
    if (this._verEditCancelTimer) {
      clearTimeout(this._verEditCancelTimer);
      this._verEditCancelTimer = null;
    }
    const snap = this._verCardEditSnap;
    if (snap && this._msVerDraft) {
      const d = this._msVerDraft.find((x) => x.patch === snap.patch);
      if (d) {
        d.title = snap.title;
        d.problem = snap.problem;
      }
    }
    this._verCardEdit = null;
    this._verCardEditSnap = null;
  },

  beginMsVerCardEdit(patch) {
    if (this._verEditCancelTimer) {
      clearTimeout(this._verEditCancelTimer);
      this._verEditCancelTimer = null;
    }
    if (this._verCardEdit && this._verCardEdit !== patch) {
      this.cancelMsVerCardEdit();
    }
    const d = (this._msVerDraft || []).find((x) => x.patch === patch);
    this._verCardEditSnap = d
      ? { patch, title: d.title, problem: d.problem }
      : { patch, title: "", problem: "" };
    this._verCardEdit = patch;
  },

  bindMsVerCards(el, route, editing) {
    if (!editing) return;
    const list = el.querySelector("#ms-ver-cards");
    if (!list) return;

    list.querySelectorAll(".del-ms-ver-card").forEach((card) => {
      card.addEventListener("click", (e) => {
        if (!this._verCardEdit) return;
        if (e.target.closest("button, input, textarea")) return;
        const patch = Number(card.getAttribute("data-patch"));
        if (!patch || patch === this._verCardEdit) return;
        // Defer so a double-click can open edit on the other card instead.
        if (this._verEditCancelTimer) clearTimeout(this._verEditCancelTimer);
        this._verEditCancelTimer = setTimeout(() => {
          this._verEditCancelTimer = null;
          if (!this._verCardEdit || this._verCardEdit === patch) return;
          this.cancelMsVerCardEdit();
          this.render();
        }, 220);
      });
      card.addEventListener("dblclick", (e) => {
        if (e.target.closest("input, textarea, button")) return;
        const patch = Number(card.getAttribute("data-patch"));
        if (!patch) return;
        if (this._verCardEdit === patch) return;
        this.beginMsVerCardEdit(patch);
        this.render();
      });
      card.addEventListener("dragstart", (e) => {
        if (e.target.closest("button, input, textarea, .del-ms-ver-edit-actions")) {
          e.preventDefault();
          return;
        }
        if (this._verEditCancelTimer) {
          clearTimeout(this._verEditCancelTimer);
          this._verEditCancelTimer = null;
        }
        this.captureMsVerCardEdits(el);
        this._verCardEdit = null;
        this._verCardEditSnap = null;
        const patch = card.getAttribute("data-patch");
        e.dataTransfer.setData("text/plain", patch);
        e.dataTransfer.effectAllowed = "move";
        card.classList.add("dragging");
      });
      card.addEventListener("dragend", () => {
        card.classList.remove("dragging");
        if (!this._verCardEdit) this.render();
      });
      card.addEventListener("dragover", (e) => {
        e.preventDefault();
        e.dataTransfer.dropEffect = "move";
        card.classList.add("drag-over");
      });
      card.addEventListener("dragleave", () => card.classList.remove("drag-over"));
      card.addEventListener("drop", (e) => {
        e.preventDefault();
        card.classList.remove("drag-over");
        const fromPatch = Number(e.dataTransfer.getData("text/plain"));
        const toPatch = Number(card.getAttribute("data-patch"));
        if (!fromPatch || !toPatch || fromPatch === toPatch) return;
        this.captureMsVerCardEdits(el);
        this.swapMsVerDraftContent(fromPatch, toPatch);
        this._verCardEdit = null;
        this._verCardEditSnap = null;
        this.render();
      });
    });

    list.querySelectorAll("[data-ver-done-edit]").forEach((btn) => {
      btn.addEventListener("click", () => {
        this.captureMsVerCardEdits(el);
        this._verCardEdit = null;
        this._verCardEditSnap = null;
        if (this._verEditCancelTimer) {
          clearTimeout(this._verEditCancelTimer);
          this._verEditCancelTimer = null;
        }
        this.render();
      });
    });
    list.querySelectorAll("[data-ver-cancel-edit]").forEach((btn) => {
      btn.addEventListener("click", () => {
        this.cancelMsVerCardEdit();
        this.render();
      });
    });
    list.querySelectorAll("[data-ver-delete]").forEach((btn) => {
      btn.addEventListener("pointerdown", (e) => e.stopPropagation());
      btn.addEventListener("mousedown", (e) => e.stopPropagation());
      btn.addEventListener("dragstart", (e) => {
        e.preventDefault();
        e.stopPropagation();
      });
      btn.addEventListener("click", (e) => {
        e.preventDefault();
        e.stopPropagation();
        const patch = Number(btn.getAttribute("data-ver-delete"));
        if (!patch) return;
        const cards = list.querySelectorAll(".del-ms-ver-card");
        if (cards.length <= 1) {
          this.showUiModal(`
            <div class="modal-card">
              <h3 style="margin-top:0">Cannot delete</h3>
              <p class="muted">At least one version is required for a milestone.</p>
              <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
                <button type="button" data-close-ui>OK</button>
              </div>
            </div>`);
          return;
        }
        this.captureMsVerCardEdits(el);
        this.openDeleteVersionModal(route.engId, route.major, route.minor, patch);
      });
    });
  },

  openDeleteVersionModal(engId, major, minor, patch) {
    const key = `${major}.${minor}.${patch}`;
    const draft = (this._msVerDraft || []).find((d) => d.patch === patch);
    const title = (draft && draft.title) || "Untitled";
    const later = (this._msVerDraft || []).filter((d) => d.patch > patch).length;
    const lines = [
      `${title}  ·  ${key}`,
      "",
      "Later versions under this milestone renumber forward"
        + (later ? `: ${later} card${later === 1 ? "" : "s"} move down one patch number.` : "."),
      "This cannot be undone from Trash.",
    ];
    window.AnvilUI.modal.requireTyped(this.uiModal(), {
      title: `Delete version ${key}?`,
      body: lines.join("\n"),
      word: `DELETE ${key}`,
      confirmLabel: "Delete version",
      onConfirm: async () => {
        try {
          await api.v1.deliveryDeleteVersion(engId, major, minor, patch);
          this._verCardEdit = null;
          this._verCardEditSnap = null;
          this._msVerDraft = null;
          this._msVerDraftKey = null;
          await this.reload();
          this.render();
        } catch (e) {
          this.showNotice(e.message || String(e), "Delete failed");
        }
      },
    });
  },

  swapMsVerDraftContent(fromPatch, toPatch) {
    const draft = this._msVerDraft || [];
    const a = draft.find((d) => d.patch === fromPatch);
    const b = draft.find((d) => d.patch === toPatch);
    if (!a || !b) return;
    const tmp = { title: a.title, problem: a.problem };
    a.title = b.title;
    a.problem = b.problem;
    b.title = tmp.title;
    b.problem = tmp.problem;
    // Keep milestone title fields in sync with patch-1 slot after swap
    const head = draft.reduce((m, d) => Math.min(m, d.patch), Infinity);
    const h = draft.find((d) => d.patch === head);
    const titleEl = this.el?.querySelector("#ms-title");
    const problemEl = this.el?.querySelector("#ms-problem");
    if (h && titleEl) titleEl.value = h.title || "";
    if (h && problemEl) problemEl.value = h.problem || "";
  },

  viewMilestoneView(engId, major, minor) {
    const eng = this.findEng(engId);
    const versions = eng ? this.milestoneVersions(eng, major, minor) : [];
    const head = versions[0];
    if (!eng || !head) {
      return `<p class="err">Milestone not found.</p>
        <p><a href="#/delivery/requirements/${this.esc(engId)}">Back</a></p>`;
    }
    const w = this.findWorker(eng.worker_id);
    const msKey = `${major}.${minor}`;
    const editKey = `${engId}:${major}.${minor}`;
    const canEdit = this.isAwaitingStatus(head.status);
    const editing = this._msEditing === editKey;
    const ro = editing ? "" : "readonly";
    if (editing) this.ensureMsVerDraft(editKey, versions);
    const draft = editing ? this._msVerDraft : null;
    const verCards = versions
      .map((v) => {
        const d =
          (draft && draft.find((x) => x.patch === v.patch)) || {
            title: v.title,
            problem: v.problem || "",
          };
        const key = this.verKey(v);
        const inline = editing && this._verCardEdit === v.patch;
        const body = inline
          ? `<input class="del-ms-ver-edit-title" value="${this.esc(d.title)}" placeholder="Version title" />
             <textarea class="del-ms-ver-edit-note" rows="2" placeholder="Delivery note">${this.esc(d.problem)}</textarea>
             <div class="del-ms-ver-edit-actions">
               <button type="button" class="ghost" data-ver-cancel-edit>Cancel</button>
               <button type="button" class="ghost del-ms-ver-done" data-ver-done-edit>Done</button>
             </div>`
          : `<strong class="del-ms-ver-title">${this.esc(d.title || "Untitled")}</strong>
             ${d.problem ? `<span class="muted del-ms-ver-note">${this.esc(d.problem)}</span>` : ""}
             ${editing ? `<span class="muted del-ms-ver-hint">Double-click to edit · drag to swap content</span>` : ""}`;
        const canDelete = editing && versions.length > 1;
        const delX = canDelete
          ? `<button type="button" class="ghost del-ms-ver-x" data-ver-delete="${v.patch}" title="Delete version" draggable="false">×</button>`
          : "";
        const viewDrag = !editing;
        return `
      <div class="del-ms-ver-card${editing ? " editable" : ""}${canDelete ? " has-delete" : ""}${viewDrag ? " cite-drag" : ""}"
        data-ver-key="${this.esc(key)}" data-patch="${v.patch}"
        id="ms-ver-${this.esc(key.replace(/\./g, "-"))}"
        ${editing || viewDrag ? 'draggable="true"' : ""}
        ${viewDrag ? 'data-cite-drag="1"' : ""}>
        <code class="del-ms-ver-fixed">${this.esc(key)}</code>
        <div class="del-ms-ver-body">${body}</div>
        ${delX}
      </div>`;
      })
      .join("");
    const actions = [];
    if (canEdit && !editing) {
      actions.push(`<button type="button" class="ghost" id="ms-edit">Edit</button>`);
    }
    if (editing) {
      actions.push(`<button type="button" class="ghost" id="ms-cancel-edit">Cancel</button>`);
      actions.push(`<button type="button" id="ms-save">Save</button>`);
    }
    if (!editing && !this.majorIsSealed(major)) {
      actions.push(
        `<button type="button" class="ghost danger" id="ms-delete">Delete</button>`,
      );
    }
    const viewDrag = !editing;
    const fieldWrap = (key, label, control) => `
      <div class="del-cite-field${viewDrag ? " cite-drag" : ""}" data-cite-key="${this.esc(key)}"
        ${viewDrag ? 'draggable="true"' : ""}>
        <label>${label}</label>
        ${control}
      </div>`;
    const chat = this.polishChat(`ms:${editKey}`, {
      id: "ai-chat-ms",
      title: "Polish chat",
      placeholder: "Message… (Enter to send, Shift+Enter for newline)",
      tall: true,
      rows: 3,
      allowDrop: viewDrag,
      citeNav: [
        { id: "detail", label: "Detail" },
        { id: "version", label: "Version" },
      ],
      hint:
        "<strong>Detail</strong> / <strong>Version</strong> add <code>@</code> chips. " +
        (viewDrag ? "In View, drag fields or version cards onto this panel. " : "") +
        "Click a chip to jump · × to remove.",
      onCiteNav: (navId, c) => {
        if (navId === "detail") this.openCiteDetailPicker(c);
        else if (navId === "version") {
          this.openCiteVersionPicker(engId, major, minor, c);
        }
      },
      onSend: async ({ composed }, c) => {
        c.appendMessage(
          "ai",
          "(mock) Noted. Suggested polish applied to Completion criteria.",
        );
        const next =
          (head.success || "").trim() +
          (head.success ? "\n" : "") +
          "· (AI mock) " +
          composed.slice(0, 200);
        await api.v1.deliveryPatchVersion(engId, major, minor, head.patch, {
          success: next,
        });
        await this.reload();
        this.render();
      },
    });
    return `
      ${this.breadcrumbHtml([
        { label: "Generations", href: "#/delivery/requirements" },
        { label: this.majorLabel(major), href: `#/delivery/requirements/m/${major}` },
        { label: eng.title || (w && w.label) || eng.worker_id, href: `#/delivery/requirements/${eng.id}` },
        { label: msKey, href: null },
      ])}
      <div class="del-split del-split-chatwide">
        <div class="del-main">
          <div class="card">
            <div class="toolbar">
              <div style="flex:1">
                <div class="muted">Worker ${this.esc(w ? w.label : "")} · reqs <code>${this.esc(eng.reqs_rel)}</code></div>
                <h3 style="margin:0.25rem 0 0">
                  Milestone <code>${this.esc(msKey)}</code>
                </h3>
              </div>
              ${this.badge(head.status)}
              ${actions.join("")}
            </div>
            ${fieldWrap("Title", "Title", `<input id="ms-title" value="${this.esc(head.title)}" ${ro} />`)}
            ${fieldWrap("Delivery note", "Delivery note", `<textarea id="ms-problem" rows="2" ${ro}>${this.esc(head.problem)}</textarea>`)}
            ${fieldWrap("Goals", "Goals", `<textarea id="ms-goals" rows="3" ${ro}>${this.esc(head.goals)}</textarea>`)}
            ${fieldWrap("Completion criteria", "Completion criteria", `<textarea id="ms-success" rows="3" ${ro}>${this.esc(head.success)}</textarea>`)}
            <div class="toolbar del-ms-ver-head">
              <h4 style="margin:0;flex:1">Versions</h4>
              ${
                editing
                  ? `<button type="button" class="ghost" id="ms-ver-add" title="Add version">+</button>`
                  : ""
              }
            </div>
            <div class="del-ms-ver-cards" id="ms-ver-cards">${
              verCards || `<p class="muted">No versions.</p>`
            }</div>
          </div>
        </div>
        ${chat.render()}
      </div>`;
  },

  viewVersionDetail(engId, major, minor, patch) {
    const eng = this.findEng(engId);
    const ver = eng && this.findVersion(eng, major, minor, patch);
    if (!eng || !ver) {
      return `<p class="err">Version not found.</p><p><a href="#/delivery/requirements/${this.esc(engId)}">Back</a></p>`;
    }
    const w = this.findWorker(eng.worker_id);
    const key = this.verKey(ver);
    const editKey = `${engId}:${major}.${minor}.${patch}`;
    const canEdit = this.isAwaitingStatus(ver.status);
    const editing = this._msEditing === editKey;
    const ro = editing ? "" : "readonly";
    const actions = [];
    if (canEdit && !editing) {
      actions.push(`<button type="button" class="ghost" id="ver-edit">Edit</button>`);
    }
    if (editing) {
      actions.push(`<button type="button" id="ver-save">Save</button>`);
    }
    const viewDrag = !editing;
    const fieldWrap = (citeKey, label, control) => `
      <div class="del-cite-field${viewDrag ? " cite-drag" : ""}" data-cite-key="${this.esc(citeKey)}"
        ${viewDrag ? 'draggable="true"' : ""}>
        <label>${label}</label>
        ${control}
      </div>`;
    const chat = this.polishChat(`ver:${editKey}`, {
      id: "ai-chat-ver",
      title: "Polish chat",
      placeholder: "Message… (Enter to send, Shift+Enter for newline)",
      tall: true,
      rows: 3,
      allowDrop: viewDrag,
      citeNav: [
        { id: "detail", label: "Detail" },
        { id: "version", label: "Version" },
      ],
      hint:
        "<strong>Detail</strong> / <strong>Version</strong> add <code>@</code> chips. " +
        (viewDrag ? "In View, drag fields onto this panel. " : "") +
        "Click a chip to jump · × to remove.",
      onCiteNav: (navId, c) => {
        if (navId === "detail") this.openCiteDetailPicker(c);
        else if (navId === "version") {
          this.openCiteVersionPicker(engId, major, minor, c);
        }
      },
      onSend: async ({ composed }, c) => {
        c.appendMessage(
          "ai",
          "(mock) Noted. Suggested polish applied to Completion criteria.",
        );
        const next =
          (ver.success || "").trim() +
          (ver.success ? "\n" : "") +
          "· (AI mock) " +
          composed.slice(0, 200);
        await api.v1.deliveryPatchVersion(engId, major, minor, patch, {
          success: next,
        });
        await this.reload();
        this.render();
      },
    });
    return `
      ${this.breadcrumbHtml([
        { label: "Generations", href: "#/delivery/requirements" },
        { label: this.majorLabel(major), href: `#/delivery/requirements/m/${major}` },
        { label: eng.title || (w && w.label) || eng.worker_id, href: `#/delivery/requirements/${eng.id}` },
        { label: `${major}.${minor}`, href: `#/delivery/requirements/${eng.id}/${major}/${minor}` },
        { label: key, href: null },
      ])}
      <div class="del-split del-split-chatwide">
        <div class="del-main">
          <div class="card">
            <div class="toolbar">
              <div style="flex:1">
                <div class="muted">Worker ${this.esc(w ? w.label : "")} · reqs <code>${this.esc(eng.reqs_rel)}</code></div>
                <h3 style="margin:0.25rem 0 0">
                  <code>${this.esc(key)}</code>
                  <span class="muted" style="font-weight:500;font-size:0.85rem">
                    · patch→version
                  </span>
                </h3>
              </div>
              ${this.badge(ver.status)}
              ${actions.join("")}
            </div>
            ${fieldWrap("Title", "Title", `<input id="ver-title" value="${this.esc(ver.title)}" ${ro} />`)}
            ${fieldWrap("Delivery note", "Delivery note", `<textarea id="ver-problem" rows="3" ${ro}>${this.esc(ver.problem)}</textarea>`)}
            ${fieldWrap("Goals", "Goals", `<textarea id="ver-goals" rows="2" ${ro}>${this.esc(ver.goals)}</textarea>`)}
            ${fieldWrap("Completion criteria", "Completion criteria", `<textarea id="ver-success" rows="3" ${ro} placeholder="Features and capabilities that must work…">${this.esc(ver.success)}</textarea>`)}
          </div>
        </div>
        ${chat.render()}
      </div>`;
  },

  /** Milestones that do not yet have a design (for New design picker). */
});
