(function () {
  let current = null;

  function hideAll() {
    document.getElementById("view-setup").hidden = true;
    document.getElementById("view-login").hidden = true;
    document.getElementById("view-shell").hidden = true;
  }

  function showSetup() {
    hideAll();
    document.getElementById("view-setup").hidden = false;
    if (current && Features[current] && Features[current].unmount) Features[current].unmount();
    current = null;
  }

  function showLogin() {
    hideAll();
    document.getElementById("view-login").hidden = false;
    if (current && Features[current] && Features[current].unmount) Features[current].unmount();
    current = null;
  }

  function showShell() {
    hideAll();
    document.getElementById("view-shell").hidden = false;
  }

  function showSetupBlocked(msg) {
    hideAll();
    document.getElementById("view-login").hidden = false;
    document.getElementById("login-err").textContent =
      msg || "Install wizard only allowed from localhost or 192.168.x.x";
  }

  async function route() {
    let needsSetup = false;
    let setupAllowed = true;
    try {
      const st = await api.v1.setupStatus();
      needsSetup = !!st.needs_setup;
      setupAllowed = st.setup_allowed !== false;
    } catch (_) {
      // treat as not setup if status fails after install
    }
    if (needsSetup) {
      api.setToken("");
      if (!setupAllowed) {
        location.hash = "#/login";
        showSetupBlocked();
        return;
      }
      location.hash = "#/setup";
      showSetup();
      return;
    }

    let hash = location.hash || "#/clusters";
    if (hash === "#/nodes") {
      hash = "#/clusters";
      location.hash = "#/clusters";
    }
    if (hash === "#/delivery" || hash === "#/delivery/") {
      location.hash = "#/delivery/requirements";
      return;
    }
    if (hash === "#/setup") {
      location.hash = "#/login";
    }
    if (hash === "#/login" || !api.getToken()) {
      showLogin();
      return;
    }
    try {
      const me = await api.v1.me();
      document.getElementById("whoami").textContent = me.username || "";
    } catch (_) {
      api.setToken("");
      showLogin();
      return;
    }
    showShell();
    hash = location.hash || "#/clusters";
    const item = Nav.match(hash);
    Nav.render(document.getElementById("nav"), hash);
    const app = document.getElementById("app");
    // Same feature (e.g. Delivery child routes): re-route without remount/reload.
    if (current === item.feature && Features[current] && Features[current].onRoute) {
      Features[current].onRoute();
      return;
    }
    if (current && Features[current] && Features[current].unmount) {
      Features[current].unmount();
    }
    current = item.feature;
    app.innerHTML = "";
    if (Features[current] && Features[current].mount) {
      await Features[current].mount(app);
    } else {
      app.innerHTML = `<p class="muted">Unknown feature. Add to Nav.items + Features.</p>`;
    }
  }

  document.getElementById("setup-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const err = document.getElementById("setup-err");
    err.textContent = "";
    const username = document.getElementById("setup-user").value.trim();
    const password = document.getElementById("setup-pass").value;
    const password2 = document.getElementById("setup-pass2").value;
    if (password !== password2) {
      err.textContent = "passwords do not match";
      return;
    }
    if (password.length < 8) {
      err.textContent = "password must be at least 8 characters";
      return;
    }
    try {
      const res = await api.v1.setupAdmin(username, password);
      if (res.token) api.setToken(res.token);
      location.hash = "#/nodes";
      await route();
    } catch (ex) {
      err.textContent = ex.message || "setup failed";
    }
  });

  document.getElementById("login-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const err = document.getElementById("login-err");
    err.textContent = "";
    const username = document.getElementById("user").value.trim();
    const password = document.getElementById("pass").value;
    try {
      const res = await api.v1.login(username, password);
      api.setToken(res.token);
      location.hash = "#/nodes";
      await route();
    } catch (ex) {
      err.textContent = ex.message || "login failed";
    }
  });

  document.getElementById("btn-logout").addEventListener("click", async () => {
    try { await api.v1.logout(); } catch (_) {}
    api.setToken("");
    location.hash = "#/login";
    await route();
  });

  // ---------------------------------------------------------------- search
  //
  // One box for the whole planning record, in the shell rather than per
  // surface: the question "where did I write that" is not scoped to the
  // section the operator happens to be in.
  function wireGlobalSearch() {
    const input = document.getElementById("global-search");
    if (!input) return;
    let box = document.getElementById("global-search-results");
    if (!box) {
      box = document.createElement("div");
      box.id = "global-search-results";
      box.className = "gs-results";
      box.hidden = true;
      input.parentElement.appendChild(box);
    }
    let timer = null;
    let seq = 0;
    const run = async () => {
      const q = input.value.trim();
      if (!q) {
        box.hidden = true;
        box.innerHTML = "";
        return;
      }
      const mine = ++seq;
      try {
        const data = await api.v1.search(q);
        if (mine !== seq) return; // a later keystroke won
        const hits = data.hits || [];
        box.hidden = false;
        box.innerHTML = hits.length
          ? hits
              .map(
                (h) => `<a class="gs-hit" href="${escapeAttr(h.href)}">
                  <span class="gs-kind muted">${escapeAttr(h.kind)}</span>
                  <span class="gs-title">${escapeAttr(h.title || h.id)}</span>
                  <span class="gs-excerpt muted">${escapeAttr(h.excerpt || "")}</span>
                </a>`,
              )
              .join("")
          : `<p class="muted gs-none">Nothing matches “${escapeAttr(q)}”.</p>`;
      } catch (e) {
        if (mine !== seq) return;
        box.hidden = false;
        box.innerHTML = `<p class="err">${escapeAttr(e.message || String(e))}</p>`;
      }
    };
    input.addEventListener("input", () => {
      clearTimeout(timer);
      timer = setTimeout(run, 180);
    });
    input.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        input.value = "";
        box.hidden = true;
      }
    });
    // Navigating away closes the panel; a result click is a navigation.
    document.addEventListener("click", (e) => {
      if (e.target.closest?.(".side-search") || e.target.closest?.(".gs-results")) return;
      box.hidden = true;
    });
    box.addEventListener("click", (e) => {
      if (e.target.closest("a")) box.hidden = true;
    });
  }

  function escapeAttr(v) {
    return String(v ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  wireGlobalSearch();

  window.addEventListener("hashchange", () => { route(); });
  route();
})();
