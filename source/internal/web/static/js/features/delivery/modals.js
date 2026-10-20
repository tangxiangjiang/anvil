// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Destructive and edit dialogs: majors, trash, milestone/design/version deletion.
 *
 * Part of the Delivery feature object: assigns onto `window.Features.delivery`.
 */
window.Features = window.Features || {};
window.Features.delivery = window.Features.delivery || {};

Object.assign(window.Features.delivery, {
  openEditMajorModal(major) {
    const m = (this.data.majors || []).find((x) => x.major === major);
    if (!m) return;
    if (m.status === "sealed") {
      this.showUiModal(`
        <div class="modal-card">
          <h3 style="margin-top:0">Cannot edit</h3>
          <p class="muted">Sealed majors are read-only.</p>
          <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
            <button type="button" data-close-ui>OK</button>
          </div>
        </div>`);
      return;
    }
    const label = major === 0 ? "0.x" : major + ".x";
    const chat = this.polishChat("edit-major", {
      id: "ai-chat-edit-major",
      title: "AI assist",
      placeholder: "How should this generation be described?",
      tall: true,
      rows: 3,
      allowDrop: false,
      citeNav: [
        { id: "title", label: "Title" },
        { id: "summary", label: "Summary" },
      ],
      emptyHint: "Cite Title / Summary, then describe how to polish this generation.",
      onCiteNav: (navId, ch) => {
        if (navId === "title") ch.addCite("Title");
        else if (navId === "summary") ch.addCite("Summary");
      },
      onScrollCite: (key) => {
        const modal = this.uiModal();
        const map = { Title: "#del-edit-maj-title", Summary: "#del-edit-maj-sum" };
        const sel = map[key];
        const field = sel && modal?.querySelector(sel);
        if (field) {
          field.scrollIntoView({ behavior: "smooth", block: "center" });
          field.focus?.();
          window.AiChat.flash(field);
        }
      },
      // Real drafting: the proposals land in the Title / Summary fields the operator
      // is already editing. The modal's own Save is still what persists them, so a
      // suggestion is an edit suggestion and never a write.
      onSend: async ({ composed, cites }, ch) => {
        const modal = this.uiModal();
        await window.AnvilUI.assist.ask({
          surface: "major",
          instruction: composed,
          cites,
          current: {
            title: (modal?.querySelector("#del-edit-maj-title")?.value || "").trim(),
            summary: (modal?.querySelector("#del-edit-maj-sum")?.value || "").trim(),
          },
          host: modal?.querySelector("#maj-assist"),
          chat: ch,
          onApply: async (field, value) => {
            const sel = field === "title" ? "#del-edit-maj-title" : "#del-edit-maj-sum";
            const el = modal?.querySelector(sel);
            if (el) el.value = value;
          },
        });
      },    });
    this.showUiModal(`
      <div class="modal-card modal-chat del-maj-assist-modal">
        <div class="toolbar del-maj-assist-head">
          <h3 style="margin:0">Edit major ${this.esc(label)}</h3>
        </div>
        <div class="del-maj-assist-split">
          <div class="del-maj-assist-form">
            <label>Title</label>
            <input id="del-edit-maj-title" value="${this.esc(m.title || "")}" data-cite-key="Title"
              placeholder="Short name for this generation" />
            <label>Summary</label>
            <div id="maj-assist" class="assist-host"></div>
            <textarea id="del-edit-maj-sum" rows="8" data-cite-key="Summary"
              placeholder="What this generation covers, goals, and boundaries">${this.esc(m.summary || "")}</textarea>
            <div class="err" id="del-edit-maj-err"></div>
            <div class="toolbar del-maj-assist-actions">
              <button type="button" class="ghost" data-close-ui>Cancel</button>
              <button type="button" id="del-edit-maj-ok">Save</button>
            </div>
          </div>
          ${chat.render()}
        </div>
      </div>`);
    const modal = this.uiModal();
    chat.mount(modal);
    modal.querySelector("#del-edit-maj-ok").onclick = async () => {
      const err = modal.querySelector("#del-edit-maj-err");
      err.textContent = "";
      try {
        await api.v1.deliveryPatchMajor(major, {
          title: modal.querySelector("#del-edit-maj-title").value.trim(),
          summary: modal.querySelector("#del-edit-maj-sum").value,
        });
        this.closeUiModal();
        await this.reload();
        this.render();
      } catch (e) {
        err.textContent = e.message || String(e);
      }
    };
  },

  openAdvanceMajorModal(major, from, to) {
    if (to === "developing" && !this.prevMajorSealed(major)) {
      const prevLabel = major - 1 === 0 ? "0.x" : major - 1 + ".x";
      this.showNotice(
        `Seal previous major ${prevLabel} before starting this generation.`,
        "Cannot start yet",
      );
      return;
    }
    const label = major === 0 ? "0.x" : major + ".x";
    const fromL = this.majorStatusLabel(from);
    const toL = this.majorStatusLabel(to);
    const advance = async () => {
      try {
        await api.v1.deliveryAdvanceMajor(major);
        this.closeUiModal();
        await this.reload();
        this.render();
      } catch (e) {
        this.showNotice(e.message || e, "Advance failed");
      }
    };

    // Sealing is the irreversible one: a sealed generation accepts no new
    // milestones, and there is no unseal. It gets the typed confirmation.
    if (to === "sealed") {
      const m = (this.data.majors || []).find((x) => x.major === major);
      const counts = this.majorWorkCounts(major);
      const lines = [
        `${label}${m && m.title ? " — " + m.title : ""}`,
        `${fromL} → ${toL}`,
        "",
        `A sealed generation is locked for good: no new milestones can be added under it.`,
        counts
          ? `It currently holds ${counts.milestones} milestone${counts.milestones === 1 ? "" : "s"} and ${counts.requirements} requirement${counts.requirements === 1 ? "" : "s"}.`
          : "",
        "This cannot be undone.",
      ].filter(Boolean);
      window.AnvilUI.modal.requireTyped(this.uiModal(), {
        title: `Seal generation ${label}?`,
        body: lines.join("\n"),
        word: `SEAL ${label}`,
        confirmLabel: "Seal generation",
        onConfirm: advance,
      });
      return;
    }

    const detail =
      "This generation becomes the active development line. New milestones will attach here.";
    window.AnvilUI.modal.open(
      this.uiModal(),
      `
      <div class="modal-card">
        <h3 style="margin-top:0">Advance major ${this.esc(label)}?</h3>
        <p>
          <span class="badge warn">${this.esc(fromL)}</span>
          <span class="muted"> → </span>
          <span class="badge ok">${this.esc(toL)}</span>
        </p>
        <p class="muted">${this.esc(detail)}</p>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" id="del-adv-maj-ok">${this.esc(this.advanceActionLabel(to))}</button>
        </div>
      </div>`,
      { title: `Advance major ${label}` },
    );
    this.uiModal().querySelector("#del-adv-maj-ok").onclick = advance;
  },

  openAddMajorModal() {
    const next =
      (this.data.majors || []).reduce((a, m) => Math.max(a, m.major), -1) + 1;
    const label = next === 0 ? "0.x" : next + ".x";
    const chat = this.polishChat("add-major", {
      id: "ai-chat-add-major",
      title: "AI assist",
      placeholder: "What should this generation cover?",
      tall: true,
      rows: 3,
      allowDrop: false,
      citeNav: [
        { id: "title", label: "Title" },
        { id: "summary", label: "Summary" },
      ],
      emptyHint: "Cite Title / Summary, then describe this generation.",
      onCiteNav: (navId, ch) => {
        if (navId === "title") ch.addCite("Title");
        else if (navId === "summary") ch.addCite("Summary");
      },
      onScrollCite: (key) => {
        const modal = this.uiModal();
        const map = { Title: "#del-add-maj-title", Summary: "#del-add-maj-sum" };
        const sel = map[key];
        const field = sel && modal?.querySelector(sel);
        if (field) {
          field.scrollIntoView({ behavior: "smooth", block: "center" });
          field.focus?.();
          window.AiChat.flash(field);
        }
      },
      // Real drafting: the proposals land in the Title / Summary fields the operator
      // is already editing. The modal's own Save is still what persists them, so a
      // suggestion is an edit suggestion and never a write.
      onSend: async ({ composed, cites }, ch) => {
        const modal = this.uiModal();
        await window.AnvilUI.assist.ask({
          surface: "major",
          instruction: composed,
          cites,
          current: {
            title: (modal?.querySelector("#del-add-maj-title")?.value || "").trim(),
            summary: (modal?.querySelector("#del-add-maj-sum")?.value || "").trim(),
          },
          host: modal?.querySelector("#maj-assist"),
          chat: ch,
          onApply: async (field, value) => {
            const sel = field === "title" ? "#del-add-maj-title" : "#del-add-maj-sum";
            const el = modal?.querySelector(sel);
            if (el) el.value = value;
          },
        });
      },    });
    this.showUiModal(`
      <div class="modal-card modal-chat del-maj-assist-modal">
        <div class="toolbar del-maj-assist-head">
          <h3 style="margin:0">Add major</h3>
          <span class="muted">Next <code>${this.esc(label)}</code> · starts as Planning</span>
        </div>
        <div class="del-maj-assist-split">
          <div class="del-maj-assist-form">
            <label>Title</label>
            <input id="del-add-maj-title" value="" data-cite-key="Title"
              placeholder="Short name for this generation (e.g. first stable release)" />
            <label>Summary</label>
            <div id="maj-assist" class="assist-host"></div>
            <textarea id="del-add-maj-sum" rows="8" data-cite-key="Summary"
              placeholder="What this generation covers, goals, and boundaries"></textarea>
            <div class="err" id="del-add-maj-err"></div>
            <div class="toolbar del-maj-assist-actions">
              <button type="button" class="ghost" data-close-ui>Cancel</button>
              <button type="button" id="del-add-maj-ok">Add</button>
            </div>
          </div>
          ${chat.render()}
        </div>
      </div>`);
    const modal = this.uiModal();
    chat.mount(modal);
    modal.querySelector("#del-add-maj-ok").onclick = async () => {
      const err = modal.querySelector("#del-add-maj-err");
      err.textContent = "";
      try {
        await api.v1.deliveryAddMajor({
          major: next,
          title: modal.querySelector("#del-add-maj-title").value.trim(),
          summary: modal.querySelector("#del-add-maj-sum").value,
        });
        this.closeUiModal();
        await this.reload();
        this.render();
      } catch (e) {
        err.textContent = e.message || String(e);
      }
    };
  },

  async openDeleteMajorModal(major) {
    let preview = { milestone_count: 0, version_count: 0 };
    try {
      preview = await api.v1.deliveryPreviewDeleteMajor(major);
    } catch (e) {
      this.showNotice(e.message || e, "Error");
      return;
    }
    const label = major === 0 ? "0.x" : major + ".x";
    const hasMs = (preview.milestone_count || 0) > 0;
    const warn = hasMs
      ? `<p class="err" style="min-height:0">
           This major has <strong>${preview.milestone_count}</strong> milestone(s)
           (${preview.version_count} version row(s)).
           Deleting it moves those milestone plans to the <strong>Trash</strong>.
           You can restore them later from Trash.
         </p>`
      : `<p class="muted">No milestones under this major. It will be moved to Trash.</p>`;
    this.showUiModal(`
      <div class="modal-card">
        <h3 style="margin-top:0">Delete major ${this.esc(label)}?</h3>
        ${warn}
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" class="danger" id="del-del-maj-ok">Delete</button>
        </div>
      </div>`);
    this.uiModal().querySelector("#del-del-maj-ok").onclick = async () => {
      try {
        await api.v1.deliveryDeleteMajor(major);
        this.closeUiModal();
        if (this.selectedMajor === major) this.selectedMajor = null;
        await this.reload();
        this.render();
      } catch (e) {
        this.showNotice(e.message || e, "Error");
      }
    };
  },

  async openTrashModal() {
    let items = [];
    try {
      const data = await api.v1.deliveryTrash();
      items = data.items || [];
    } catch (e) {
      this.showNotice(e.message || e, "Error");
      return;
    }
    const rows = items
      .map((it) => {
        const p = it.preview || {};
        return `
        <div class="del-trash-row">
          <div style="flex:1">
            <div>${this.esc(it.label)}</div>
            <div class="muted">${this.esc(it.deleted_at || "")}
              ${p.milestone_count != null ? ` · ${p.milestone_count} milestone(s)` : ""}
            </div>
          </div>
          <button type="button" class="ghost" data-trash-restore="${this.esc(it.id)}">Restore</button>
          <button type="button" class="danger" data-trash-purge="${this.esc(it.id)}">Discard</button>
        </div>`;
      })
      .join("");
    this.showUiModal(`
      <div class="modal-card modal-wide">
        <div class="toolbar" style="margin:0 0 0.75rem;justify-content:space-between">
          <h3 style="margin:0">Trash</h3>
          <button type="button" class="ghost" data-close-ui>Close</button>
        </div>
        <p class="muted">Deleted majors and their milestone plans.</p>
        <div class="del-trash-list">${rows || `<p class="muted">Trash is empty.</p>`}</div>
      </div>`);
    this.uiModal().querySelectorAll("[data-trash-restore]").forEach((btn) => {
      btn.onclick = () => this.restoreTrashItem(btn.getAttribute("data-trash-restore"));
    });
    this.uiModal().querySelectorAll("[data-trash-purge]").forEach((btn) => {
      btn.onclick = async () => {
        try {
          await api.v1.deliveryPurgeTrash(btn.getAttribute("data-trash-purge"));
          await this.openTrashModal();
          await this.reload();
        } catch (e) {
          this.showNotice(e.message || e, "Error");
        }
      };
    });
  },

  async restoreTrashItem(id, mode) {
    try {
      const res = await api.v1.deliveryRestoreTrash(id, mode || "");
      this.closeUiModal();
      await this.reload();
      this.render();
      return res;
    } catch (e) {
      if (e.status === 409 && e.data && e.data.conflict) {
        const maj = e.data.major?.major;
        const label = maj === 0 ? "0.x" : maj + ".x";
        this.showUiModal(`
          <div class="modal-card">
            <h3 style="margin-top:0">Major conflict</h3>
            <p>${this.esc(e.data.message || "Major already exists.")}</p>
            <p class="muted">Major <code>${this.esc(label)}</code> is already in the plan.</p>
            <div class="toolbar" style="margin-top:1rem;flex-direction:column;align-items:stretch">
              <button type="button" id="del-restore-overwrite">Overwrite existing milestones</button>
              <button type="button" class="ghost" id="del-restore-append">Append to existing milestones</button>
              <button type="button" class="ghost" data-close-ui>Cancel</button>
            </div>
          </div>`);
        this.uiModal().querySelector("#del-restore-overwrite").onclick = () =>
          this.restoreTrashItem(id, "overwrite");
        this.uiModal().querySelector("#del-restore-append").onclick = () =>
          this.restoreTrashItem(id, "append");
        return;
      }
      this.showNotice(e.message || e, "Error");
    }
  },
});
