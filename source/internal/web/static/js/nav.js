window.Nav = {
  items: [
    { hash: "#/nodes", label: "Nodes", feature: "nodes" },
    { hash: "#/usage", label: "Usage", feature: "usage" },
  ],
  render(el, activeHash) {
    el.innerHTML = "";
    for (const item of this.items) {
      const a = document.createElement("a");
      a.href = item.hash;
      a.textContent = item.label;
      if (item.hash === activeHash) a.classList.add("active");
      el.appendChild(a);
    }
  },
};
