window.Nav = {
  items: [
    { hash: "#/clusters", label: "Clusters", feature: "clusters" },
    { hash: "#/workbench", label: "Workbench", feature: "workbench" },
    { hash: "#/jobs", label: "Job history", feature: "jobhistory" },
    { hash: "#/ops", label: "Ops", feature: "ops" },
    {
      hash: "#/delivery",
      label: "Delivery",
      feature: "delivery",
      children: [
        { hash: "#/delivery/requirements", label: "Requirements" },
        { hash: "#/delivery/designs", label: "Designs" },
        { hash: "#/delivery/roadmap", label: "Roadmap" },
        { hash: "#/delivery/test", label: "Test" },
        { hash: "#/delivery/readiness", label: "Readiness" },
      ],
    },
    { hash: "#/usage", label: "Usage", feature: "usage" },
  ],

  /** Manual fold overrides: hash → bool. Absent = auto (open when route under group). */
  _fold: {},

  match(hash) {
    const h = hash || "";
    for (const item of this.items) {
      if (item.children) {
        const child = item.children.find(
          (c) => h === c.hash || h.startsWith(c.hash + "/"),
        );
        if (child) {
          return { ...item, child };
        }
        if (h === item.hash || h.startsWith(item.hash + "/")) {
          return item;
        }
      } else if (h === item.hash || h.startsWith(item.hash + "/")) {
        return item;
      }
    }
    return this.items[0];
  },

  isOpen(item, activeHash) {
    const key = item.hash;
    if (Object.prototype.hasOwnProperty.call(this._fold, key)) {
      return this._fold[key];
    }
    const h = activeHash || "";
    return h === item.hash || h.startsWith(item.hash + "/");
  },

  toggle(itemHash, activeHash) {
    const item = this.items.find((i) => i.hash === itemHash);
    if (!item || !item.children) return;
    this._fold[itemHash] = !this.isOpen(item, activeHash);
  },

  render(el, activeHash) {
    el.innerHTML = "";
    const h = activeHash || "";
    for (const item of this.items) {
      if (item.children && item.children.length) {
        const open = this.isOpen(item, h);
        const group = document.createElement("div");
        group.className = "nav-group" + (open ? " open" : "");

        const row = document.createElement("div");
        row.className = "nav-group-row";

        const toggle = document.createElement("button");
        toggle.type = "button";
        toggle.className = "nav-fold";
        toggle.setAttribute("aria-expanded", open ? "true" : "false");
        toggle.title = open ? "Collapse" : "Expand";
        toggle.innerHTML = `<span class="chev">${open ? "▼" : "▶"}</span>`;
        toggle.addEventListener("click", (e) => {
          e.preventDefault();
          e.stopPropagation();
          this.toggle(item.hash, h);
          this.render(el, location.hash || h);
        });

        const parent = document.createElement("a");
        parent.href = item.children[0].hash;
        parent.className = "nav-parent";
        parent.textContent = item.label;
        const parentOn = h === item.hash || h.startsWith(item.hash + "/");
        if (parentOn) parent.classList.add("active");

        row.appendChild(toggle);
        row.appendChild(parent);
        group.appendChild(row);

        const kids = document.createElement("div");
        kids.className = "nav-children";
        kids.hidden = !open;
        for (const child of item.children) {
          const a = document.createElement("a");
          a.href = child.hash;
          a.className = "nav-child";
          a.textContent = child.label;
          const on = h === child.hash || h.startsWith(child.hash + "/");
          if (on) a.classList.add("active");
          kids.appendChild(a);
        }
        group.appendChild(kids);
        el.appendChild(group);
        continue;
      }

      const a = document.createElement("a");
      a.href = item.hash;
      a.textContent = item.label;
      const on = h === item.hash || h.startsWith(item.hash + "/");
      if (on) a.classList.add("active");
      el.appendChild(a);
    }
  },
};
