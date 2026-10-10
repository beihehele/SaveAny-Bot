"use strict";

(() => {
  const $ = (id) => document.getElementById(id);
  const pages = ["overview", "tasks", "new", "storages", "files", "routes", "preferences", "diagnostics"];
  const state = { authenticated: false, csrf: "", language: "zh-Hans", words: {}, storages: [], capabilities: [], metadata: false, page: "overview", sequence: 0, abort: null, loading: false, submitting: false, taskOffset: 0, fileOffset: 0 };
  let languageSequence = 0;
  let searchTimer, toastTimer;
  const t = (key) => state.words[key] || key;
  const node = (tag, text, className) => {
    const element = document.createElement(tag);
    if (text !== undefined && text !== null) element.textContent = String(text);
    if (className) element.className = className;
    return element;
  };
  const date = (value) => value ? new Date(value).toLocaleString(state.language) : "—";
  const bytes = (value) => {
    const size = Number(value || 0);
    if (size < 1024) return `${size} B`;
    const units = ["KB", "MB", "GB", "TB"];
    const exponent = Math.min(Math.floor(Math.log(size) / Math.log(1024)), 4);
    return `${(size / 1024 ** exponent).toFixed(1)} ${units[exponent - 1]}`;
  };
  function duration(seconds) {
    const hours = Math.floor(seconds / 3600);
    const minutes = Math.floor(seconds % 3600 / 60);
    return hours ? `${hours} ${t("hours")} ${minutes} ${t("minutes")}` : `${minutes} ${t("minutes")}`;
  }
  function toast(message) {
    clearTimeout(toastTimer);
    $("toast").textContent = message;
    $("toast").hidden = false;
    toastTimer = setTimeout(() => { $("toast").hidden = true; }, 4500);
  }
  function showLogin(message = "") {
    state.authenticated = false;
    state.csrf = "";
    state.metadata = false;
    state.storages = [];
    state.capabilities = [];
    state.sequence++;
    if (state.abort) state.abort.abort();
    $("console").hidden = true;
    $("login-view").hidden = false;
    $("boot").hidden = true;
    $("password").value = "";
    $("login-error").textContent = message;
    $("login-error").hidden = !message;
    $("task-dialog").close();
    $("password").focus();
  }
  async function request(path, options = {}) {
    const headers = new Headers(options.headers);
    if (options.body !== undefined) headers.set("Content-Type", "application/json");
    if (state.csrf) headers.set("X-CSRF-Token", state.csrf);
    const response = await fetch(`/admin/api/v1/${path}`, { ...options, headers, credentials: "same-origin", cache: "no-store" });
    const data = await response.json();
    if (!response.ok) {
      if (response.status === 401 && path !== "login") showLogin(t("sessionExpired"));
      throw new Error(t(data.error || "internal_error"));
    }
    return data;
  }
  async function language(value) {
    const sequence = ++languageSequence;
    state.language = value === "en" ? "en" : "zh-Hans";
    const response = await fetch(`/admin/assets/locales/${state.language}.json`, { cache: "no-store" });
    if (!response.ok) throw new Error("Unable to load language resources");
    const words = await response.json();
    if (sequence !== languageSequence) return;
    state.words = words;
    document.documentElement.lang = state.language;
    document.title = `SaveAny · ${t("consoleName")}`;
    $("language").value = state.language;
    document.querySelectorAll("[data-i18n]").forEach((element) => { element.textContent = t(element.dataset.i18n); });
    document.querySelectorAll("[data-i18n-placeholder]").forEach((element) => { element.placeholder = t(element.dataset.i18nPlaceholder); });
    document.querySelectorAll("[data-i18n-aria-label]").forEach((element) => { element.setAttribute("aria-label", t(element.dataset.i18nAriaLabel)); });
    try { localStorage.setItem("saveany.admin.language", state.language); } catch { /* Language persistence is optional. */ }
    if (state.authenticated) await loadPage();
  }
  function definitions(target, values) {
    target.replaceChildren();
    for (const [label, value] of values) {
      target.append(node("dt", label), node("dd", typeof value === "boolean" ? t(value ? "yes" : "no") : (value ?? "—")));
    }
  }
  function badge(status) { return node("span", t(status), `badge ${status}`); }
  function button(label, action, className = "secondary small-button") {
    const result = node("button", label, className);
    result.type = "button";
    result.addEventListener("click", action);
    return result;
  }
  function table(target, labels, rows) {
    target.replaceChildren();
    if (!rows.length) { target.append(node("p", t("empty"), "empty")); return; }
    const wrap = node("div", null, "table-wrap");
    const result = node("table");
    const header = node("thead"), headerRow = node("tr"), body = node("tbody");
    labels.forEach((label) => headerRow.append(node("th", t(label))));
    header.append(headerRow);
    rows.forEach((cells) => {
      const row = node("tr");
      cells.forEach((value) => {
        const cell = node("td");
        cell.append(value instanceof Node ? value : node("span", value ?? "—"));
        row.append(cell);
      });
      body.append(row);
    });
    result.append(header, body); wrap.append(result); target.append(wrap);
  }
  function pagination(target, total, offset, action) {
    target.replaceChildren(node("span", `${total ? offset + 1 : 0}–${Math.min(offset + 100, total)} / ${total}`));
    const previous = button(t("previousPage"), () => action(Math.max(0, offset - 100)));
    previous.disabled = offset === 0;
    const next = button(t("nextPage"), () => action(offset + 100));
    next.disabled = offset + 100 >= total;
    target.append(previous, next);
  }
  function taskDetails(task) {
    const values = [[t("taskID"), task.task_id], [t("title"), task.title], [t("status"), t(task.status)],
      [t("scope"), t(task.scope === "tracked" ? "trackedScope" : "queueScope")], [t("taskType"), t(task.type || "unknownType")],
      [t("storage"), task.storage], [t("path"), task.path], [t("createdAt"), date(task.created_at)]];
    if (task.progress) values.push([t("progress"), `${Number(task.progress.percent || 0).toFixed(1)}%`],
      [t("downloaded"), `${bytes(task.progress.downloaded_bytes)} / ${bytes(task.progress.total_bytes)}`],
      [t("fileCount"), `${task.progress.downloaded_files || 0} / ${task.progress.total_files || 0}`]);
    if (task.result_summary) values.push([t("resultSummary"), JSON.stringify(task.result_summary)]);
    definitions($("task-details"), values);
    $("task-detail-error").textContent = task.error || "";
    $("task-detail-error").hidden = !task.error;
    if (!$("task-dialog").open) $("task-dialog").showModal();
  }
  async function cancelTask(task, trigger) {
    if (!window.confirm(t("confirmCancel"))) return;
    trigger.disabled = true;
    try {
      const result = await request(`tasks/${encodeURIComponent(task.task_id)}/cancel`, { method: "POST" });
      toast(t(result.status));
      await loadPage();
    } catch (error) { toast(error.message); } finally { trigger.disabled = false; }
  }
  function tasksTable(target, tasks) {
    table(target, ["task", "status", "progress", "storage", "createdAt", "actions"], tasks.map((task) => {
      const title = node("div");
      title.append(node("div", task.title || task.task_id, "cell-title"), node("div", `${task.task_id} · ${t(task.scope === "tracked" ? "trackedScope" : "queueScope")}`, "cell-subtitle"));
      const progress = node("div", null, "progress");
      if (task.progress && (task.progress.total_bytes || task.progress.total_files)) {
        const percent = Math.min(100, Math.max(0, Number(task.progress.percent || 0)));
        const meter = node("progress"); meter.max = 100; meter.value = percent;
        meter.setAttribute("aria-label", `${t("progress")} ${percent.toFixed(1)}%`);
        progress.append(node("span", `${percent.toFixed(1)}%`), meter, node("span", `${bytes(task.progress.downloaded_bytes)} · ${Number(task.progress.speed_mbps || 0).toFixed(1)} MB/s`, "cell-subtitle"));
      } else { progress.textContent = "—"; }
      const actions = node("div", null, "table-actions");
      actions.append(button(t("details"), () => taskDetails(task)));
      if (task.can_cancel) {
        const cancel = button(t("cancel"), () => cancelTask(task, cancel), "danger-button small-button");
        actions.append(cancel);
      }
      return [title, badge(task.status), progress, task.storage || "—", date(task.created_at), actions];
    }));
  }
  function storageSelectors() {
    for (const id of ["target-storage", "file-storage"]) {
      const select = $(id), selected = select.value;
      const storages = id === "file-storage" ? state.storages.filter((storage) => storage.type === "local" && storage.listable) : state.storages;
      select.replaceChildren();
      storages.forEach((storage) => { const option = node("option", storage.name); option.value = storage.name; select.append(option); });
      if (storages.some((storage) => storage.name === selected)) select.value = selected;
      select.disabled = !storages.length;
    }
  }
  function taskTypes() {
    const selected = $("task-type").value;
    $("task-type").replaceChildren();
    state.capabilities.forEach((capability) => {
      const option = node("option", `${t(capability.type)}${capability.available ? "" : ` · ${t("unavailable")}`}`);
      option.value = capability.type; option.disabled = !capability.available;
      $("task-type").append(option);
    });
    const valid = state.capabilities.find((capability) => capability.type === selected && capability.available);
    $("task-type").value = valid ? selected : (state.capabilities.find((capability) => capability.available)?.type || "");
    typeFields();
  }
  function typeFields() {
    const type = $("task-type").value;
    $("task-urls").required = true;
    $("urls-label").textContent = t("messageLinks");
    $("task-type-note").textContent = t("urlsNote");
    $("task-submit").disabled = state.submitting || !type || !state.storages.length;
  }
  function capabilityList() {
    $("overview-capabilities").replaceChildren();
    state.capabilities.forEach((capability) => {
      const row = node("div", null, "capability-row");
      row.append(node("span", t(capability.type)), badge(capability.available ? "available" : "unavailable"));
      $("overview-capabilities").append(row);
    });
  }
  function storageCards() {
    $("storage-cards").replaceChildren();
    if (!state.storages.length) { $("storage-cards").append(node("p", t("empty"), "empty")); return; }
    state.storages.forEach((storage) => {
      const card = node("article", null, "panel storage-card");
      card.append(node("h2", storage.name), node("span", storage.type, "badge"));
      const values = node("dl", null, "definition-list");
      definitions(values, [[t("readable"), storage.readable], [t("listable"), storage.listable], [t("stream"), storage.stream], [t("detectExistence"), storage.detect_existence]]);
      card.append(values);
      if (storage.type === "local" && storage.listable) card.append(button(t("browseFiles"), () => {
        $("file-storage").value = storage.name; $("file-path").value = "/"; state.fileOffset = 0; location.hash = "files";
      }));
      $("storage-cards").append(card);
    });
  }
  function userPreferences(users) {
    $("preference-cards").replaceChildren();
    if (!users.length) { $("preference-cards").append(node("p", t("empty"), "empty")); return; }
    users.forEach((user) => {
      const panel = node("article", null, "panel"); panel.append(node("h2", `${t("telegramUser")} ${user.chat_id}`));
      const values = node("dl", null, "definition-list");
      const defaultDir = user.directories.find((directory) => directory.id === user.default_dir);
      definitions(values, [[t("defaultStorage"), user.default_storage || t("unset")], [t("defaultDirectory"), defaultDir?.path || t("unset")],
        [t("silent"), user.silent], [t("applyRules"), user.apply_rule], [t("filenameStrategy"), user.filename_strategy || t("unset")],
        [t("filenameTemplate"), user.filename_template || t("unset")], [t("conflictStrategy"), user.conflict_strategy || t("unset")]]);
      panel.append(values, node("h2", t("directories")));
      const dirs = node("div"); table(dirs, ["storage", "path"], user.directories.map((directory) => [directory.storage, directory.path]));
      panel.append(dirs, node("h2", t("rules")));
      const rules = node("div"); table(rules, ["ruleType", "ruleData", "storage", "path"], user.rules.map((rule) => [rule.type, rule.data, rule.storage, rule.path]));
      panel.append(rules); $("preference-cards").append(panel);
    });
  }
  function userRoutes(users) {
    $("route-table").replaceChildren();
    if (!users.length) { $("route-table").append(node("p", t("empty"), "empty")); return; }
    users.forEach((user) => {
      const panel = node("article", null, "panel"); panel.append(node("h2", `${t("telegramUser")} ${user.chat_id}`));
      const target = node("div");
      table(target, ["sourceChat", "targetChat", "topic", "filter"], user.routes.map((route) =>
        [`${route.source || "—"} (${route.source_id})`, route.target_id ? `${route.target || "—"} (${route.target_id})` : t("localSaving"),
          route.topic_id ? `${route.topic || "—"} (${route.topic_id})` : "—", route.filter || t("none")]));
      panel.append(target); $("route-table").append(panel);
    });
  }
  function fileTable(data) {
    $("file-path").value = data.path;
    table($("file-table"), ["name", "size", "modifiedAt", "actions"], data.files.map((file) => {
      const title = node("div", null, "cell-title"); title.append(node("span", `${file.is_dir ? "▱" : "▤"} ${file.name}`));
      let action;
      if (file.is_dir) action = button(t("openDirectory"), () => {
        $("file-path").value = file.path; state.fileOffset = 0; loadPage();
      });
      else {
        action = node("a", t("download"), "button secondary small-button");
        action.href = `/admin/api/v1/download?${new URLSearchParams({ storage: $("file-storage").value, path: file.path })}`;
        action.setAttribute("download", file.name);
      }
      return [title, file.is_dir ? "—" : bytes(file.size), date(file.modified_at), action];
    }));
    pagination($("file-pagination"), data.total, data.offset, (offset) => { state.fileOffset = offset; loadPage(); });
  }
  async function loadPage() {
    if (!state.authenticated) return;
    const sequence = ++state.sequence;
    if (state.abort) state.abort.abort();
    state.abort = new AbortController();
    const signal = state.abort.signal;
    state.loading = true;
    const page = pages.includes(location.hash.slice(1)) ? location.hash.slice(1) : "overview";
    state.page = page;
    $("refresh").disabled = true;
    $(`page-${page}`).setAttribute("aria-busy", "true");
    $("page-title").textContent = t(page);
    pages.forEach((key) => { $(`page-${key}`).hidden = key !== page; });
    document.querySelectorAll("[data-page]").forEach((element) => {
      element.classList.toggle("active", element.dataset.page === page);
      if (element.dataset.page === page) element.setAttribute("aria-current", "page"); else element.removeAttribute("aria-current");
    });
    $("page-error").hidden = true;
    try {
      if (!state.metadata) {
        const [storages, types] = await Promise.all([request("storages", { signal }), request("task-types", { signal })]);
        if (sequence !== state.sequence) return;
        state.storages = storages.storages; state.capabilities = types.capabilities; state.metadata = true;
        storageSelectors(); taskTypes();
      }
      if (page === "overview") {
        const [overview, tasks] = await Promise.all([request("overview", { signal }), request("tasks?limit=5", { signal })]);
        if (sequence !== state.sequence) return;
        $("overview-metrics").replaceChildren();
        [["runningTasks", overview.running, "workerNote"], ["queuedTasks", overview.queued, "queueNote"], ["loadedStorages", overview.storages, "loadedNote"], ["consoleUptime", duration(overview.uptime_seconds), "uptimeNote"]].forEach(([label, value, subtitle]) => {
          const metric = node("article", null, "metric"); metric.append(node("span", t(label), "metric-label"), node("div", value, "metric-value"), node("span", t(subtitle), "metric-subtitle")); $("overview-metrics").append(metric);
        });
        definitions($("service-information"), [[t("version"), overview.version], [t("commit"), overview.commit], [t("buildTime"), overview.build_time], [t("platform"), overview.platform], [t("goVersion"), overview.go_version], [t("workers"), overview.workers]]);
        capabilityList(); tasksTable($("overview-tasks"), tasks.tasks);
      } else if (page === "tasks") {
        const data = await request(`tasks?${new URLSearchParams({ q: $("task-search").value, status: $("task-status").value, offset: String(state.taskOffset) })}`, { signal });
        if (sequence !== state.sequence) return;
        tasksTable($("task-table"), data.tasks);
        pagination($("task-pagination"), data.total, data.offset, (offset) => { state.taskOffset = offset; loadPage(); });
      } else if (page === "storages") { storageCards(); }
      else if (page === "new") { storageSelectors(); taskTypes(); }
      else if (page === "files") {
        if (!$("file-storage").value) { $("file-table").replaceChildren(node("p", t("noLocalStorage"), "empty")); $("file-pagination").replaceChildren(); }
        else {
          const data = await request(`files?${new URLSearchParams({ storage: $("file-storage").value, path: $("file-path").value, offset: String(state.fileOffset) })}`, { signal });
          if (sequence !== state.sequence) return; fileTable(data);
        }
      } else if (page === "routes" || page === "preferences") {
        const data = await request("preferences", { signal });
        if (sequence !== state.sequence) return;
        if (page === "routes") userRoutes(data.users); else userPreferences(data.users);
      } else if (page === "diagnostics") {
        const data = await request("diagnostics", { signal });
        if (sequence !== state.sequence) return;
        definitions($("runtime-settings"), Object.entries(data.settings).map(([key, value]) => [t(`setting_${key}`), value]));
        table($("audit-table"), ["time", "operation", "object", "result"], data.audit.map((entry) => [date(entry.time), t(`audit_${entry.action}`), entry.object || "—", t(entry.result)]));
      }
      if (sequence === state.sequence) $("last-refresh").textContent = `${t("lastRefreshed")} ${new Date().toLocaleTimeString(state.language)}`;
    } catch (error) {
      if (error.name !== "AbortError" && sequence === state.sequence && state.authenticated) {
        $("page-error").textContent = error.message; $("page-error").hidden = false;
      }
    } finally { if (sequence === state.sequence) { state.loading = false; $("refresh").disabled = false; $(`page-${page}`).removeAttribute("aria-busy"); } }
  }
  $("login-form").addEventListener("submit", async (event) => {
    event.preventDefault(); $("login-submit").disabled = true; $("login-error").hidden = true;
    try {
      const data = await request("login", { method: "POST", body: JSON.stringify({ username: "admin", password: $("password").value }) });
      $("password").value = ""; state.csrf = data.csrf_token; state.authenticated = true;
      $("login-view").hidden = true; $("console").hidden = false; await loadPage();
    } catch (error) { $("login-error").textContent = error.message; $("login-error").hidden = false; }
    finally { $("login-submit").disabled = false; }
  });
  $("logout").addEventListener("click", async () => {
    $("logout").disabled = true;
    try { await request("logout", { method: "POST" }); showLogin(); }
    catch (error) { if (state.authenticated) toast(error.message); }
    finally { $("logout").disabled = false; }
  });
  $("task-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    if (state.submitting) return;
    const type = $("task-type").value, storage = $("target-storage").value, targetPath = $("target-path").value;
    const urls = $("task-urls").value.split(/\r?\n/).map((value) => value.trim()).filter(Boolean);
    if (!urls.length) { toast(t("enterUrls")); return; }
    const payload = { type, storage, path: targetPath, params: { message_links: urls } };
    state.submitting = true;
    $("task-submit").disabled = true;
    try {
      const response = await request("tasks", { method: "POST", body: JSON.stringify(payload) });
      if (!state.authenticated) return;
      toast(`${t("taskAccepted")} · ${response.task_id}`); $("task-urls").value = "";
      state.taskOffset = 0; $("task-search").value = ""; $("task-status").value = "";
      if (location.hash === "#tasks") await loadPage(); else location.hash = "tasks";
    } catch (error) { toast(error.message); }
    finally { state.submitting = false; typeFields(); }
  });
  $("task-type").addEventListener("change", typeFields);
  $("task-search").addEventListener("input", () => { clearTimeout(searchTimer); searchTimer = setTimeout(() => { state.taskOffset = 0; loadPage(); }, 350); });
  $("task-status").addEventListener("change", () => { state.taskOffset = 0; loadPage(); });
  $("file-form").addEventListener("submit", (event) => { event.preventDefault(); state.fileOffset = 0; loadPage(); });
  $("file-storage").addEventListener("change", () => { $("file-path").value = "/"; state.fileOffset = 0; loadPage(); });
  $("file-up").addEventListener("click", () => { const segments = $("file-path").value.replace(/\\/g, "/").split("/").filter(Boolean); segments.pop(); $("file-path").value = `/${segments.join("/")}`; state.fileOffset = 0; loadPage(); });
  $("close-dialog").addEventListener("click", () => $("task-dialog").close());
  $("refresh").addEventListener("click", () => { state.metadata = false; loadPage(); });
  $("language").addEventListener("change", () => language($("language").value).catch((error) => toast(error.message)));
  window.addEventListener("hashchange", loadPage);
  // Leave focused row controls and the detail dialog stable while they are in use.
  setInterval(() => {
    if (state.authenticated && !state.loading && !document.hidden && !$("task-dialog").open &&
        !document.activeElement?.closest(".table-wrap") && ["overview", "tasks"].includes(state.page)) loadPage();
  }, 3000);
  async function boot() {
    let preferred;
    try { preferred = localStorage.getItem("saveany.admin.language"); } catch { /* Browser storage can be disabled. */ }
    await language(preferred || (navigator.language.startsWith("zh") ? "zh-Hans" : "en"));
    try {
      const session = await request("session"); state.csrf = session.csrf_token; state.authenticated = true;
      $("boot").hidden = true; $("login-view").hidden = true; $("console").hidden = false; await loadPage();
    } catch { showLogin(); }
  }
  boot().catch(() => { $("boot").textContent = "SaveAny · Unable to load the console. Reload to retry."; });
})();
