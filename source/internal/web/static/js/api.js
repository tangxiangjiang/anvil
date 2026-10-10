window.api = {
  tokenKey: "anvil_web_token",
  getToken() { return localStorage.getItem(this.tokenKey) || ""; },
  setToken(t) {
    if (t) localStorage.setItem(this.tokenKey, t);
    else localStorage.removeItem(this.tokenKey);
  },
  async req(method, path, body) {
    const headers = { Accept: "application/json" };
    const tok = this.getToken();
    if (tok) headers.Authorization = "Bearer " + tok;
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const res = await fetch(path, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    const text = await res.text();
    let data = null;
    try { data = text ? JSON.parse(text) : null; } catch (_) { data = { raw: text }; }
    if (!res.ok) {
      const err = new Error((data && (data.error || data.message)) || text || res.statusText);
      err.status = res.status;
      throw err;
    }
    return data;
  },
  v1: {
    setupStatus() { return api.req("GET", "/api/v1/setup/status"); },
    setupAdmin(username, password) {
      return api.req("POST", "/api/v1/setup/admin", { username, password });
    },
    login(username, password) {
      return api.req("POST", "/api/v1/auth/login", { username, password });
    },
    logout() { return api.req("POST", "/api/v1/auth/logout", {}); },
    me() { return api.req("GET", "/api/v1/auth/me"); },
    nodes() { return api.req("GET", "/api/v1/nodes"); },
    patchNode(id, body) { return api.req("PATCH", "/api/v1/nodes/" + encodeURIComponent(id), body); },
    usage() { return api.req("GET", "/api/v1/usage"); },
  },
};
