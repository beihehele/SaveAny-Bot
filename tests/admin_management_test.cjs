const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const context = vm.createContext({ TextEncoder });
vm.runInContext(fs.readFileSync(path.join(__dirname, "../admin/static/management.js"), "utf8"), context);
const filter = context.SaveAnyManagement.keywordFilter;
const matchingChats = context.SaveAnyManagement.matchingChats;

test("source dropdown searches literal names and canonical IDs", () => {
  const chats = [{id:-1000000000011,title:"旅行 A"},{id:-1000000000012,title:"Travel B"}];
  assert.equal(matchingChats(chats, "旅行").length, 1);
  assert.equal(matchingChats(chats, "  TRAVEL ")[0].id, -1000000000012);
  assert.equal(matchingChats(chats, "-1000000000011")[0].title, "旅行 A");
  assert.equal(matchingChats(chats, "[.*]").length, 0);
  assert.equal(matchingChats(chats, "").length, 2);
});

test("keywords stay literal, phrases and exclusion compose with existing syntax", () => {
  assert.equal(filter("any", "旅行\n风景\n旅行", "广告\n推广"), 'msgre:("旅行"|"风景")&!("广告"|"推广")');
  assert.equal(filter("all", "hello world\nC++ (!test)", ""), 'msgre:("hello world"&"C++ (!test)")');
  assert.equal(filter("any", "", "广告"), 'msgre:!("广告")');
  assert.equal(filter("any", " \n", ""), "");
});

test("compiler respects current parser budgets and unsupported quoted terms", () => {
  assert.throws(() => filter("any", Array.from({length:17},(_,i)=>String(i)).join("\n"), ""), /keywordLimit/);
  assert.throws(() => filter("any", "中".repeat(90), ""), /filterTooLong/);
  assert.throws(() => filter("any", 'a"b', ""), /keywordQuote/);
  assert.equal(filter("any", "中".repeat(80), "").startsWith("msgre:"), true);
});

test("task list and details show download progress before upload results", () => {
  class Element {
    constructor() { this.children = []; this.valueText = ""; this.dataset = {}; }
    set textContent(value) { this.valueText = String(value); this.children = []; }
    get textContent() { return this.valueText + this.children.map((child) => child.textContent || "").join(" "); }
    append(...children) { this.children.push(...children); }
    replaceChildren(...children) { this.children = children; this.valueText = ""; }
    addEventListener() {}
    setAttribute() {}
    showModal() { this.open = true; }
  }
  const elements = new Map();
  const get = (id) => { if (!elements.has(id)) elements.set(id, new Element()); return elements.get(id); };
  const app = vm.createContext({ Node: Element, document: {getElementById:get, createElement:()=>new Element()} });
  const source = fs.readFileSync(path.join(__dirname, "../admin/static/app.js"), "utf8");
  const boundary = source.indexOf("  const preferences =");
  assert.ok(boundary > 0);
  vm.runInContext(source.slice(0, boundary) + "globalThis.renderTasks=tasksTable;globalThis.details=taskDetails;globalThis.words=state.words;})();", app);
  Object.assign(app.words, JSON.parse(fs.readFileSync(path.join(__dirname, "../admin/static/locales/en.json"), "utf8")));
  const task = {task_id:"album", type:"tgfiles", status:"running", downloading:true,
    progress:{total_bytes:1000, downloaded_bytes:250, percent:25},
    result_summary:{total:4,succeeded:0,failed:0,pending:4},
    upload_files:[{status:"preparing",path:"/one",size:1000,uploaded_bytes:0}]};
  const list = new Element();
  app.renderTasks(list, [task]);
  assert.match(list.textContent, /Download progress: 25\.0%/);
  app.details(task);
  assert.match(get("task-details").textContent, /25\.0%/);
  assert.doesNotMatch(get("task-details").textContent, /Downloading · 0 B/);
  task.downloading = false;
  delete task.progress;
  task.upload_files[0].status = "queued";
  app.renderTasks(list, [task]);
  app.details(task);
  assert.doesNotMatch(list.textContent, /Download progress/);
  assert.doesNotMatch(get("task-details").textContent, /25\.0%/);
});

test("task actions have only two buttons and execute without confirmation", async () => {
  const all = [];
  class Element {
    constructor(tag = "div") { this.tag = tag; this.children = []; this.dataset = {}; this.valueText = ""; this.listeners = {}; all.push(this); }
    set textContent(value) { this.valueText = String(value); this.children = []; }
    get textContent() { return this.valueText + this.children.map((child) => child.textContent || "").join(" "); }
    append(...children) { children.forEach((child) => { child.parent = this; }); this.children.push(...children); }
    replaceChildren(...children) { this.children = []; this.valueText = ""; this.append(...children); }
    closest(tag) { return this.tag === tag ? this : this.parent?.closest(tag); }
    remove() { if (this.parent) this.parent.children = this.parent.children.filter((child) => child !== this); this.parent = null; }
    addEventListener(event, action) { this.listeners[event] = action; }
    setAttribute() {}
    showModal() { this.open = true; }
    close() { this.open = false; }
  }
  const elements = new Map();
  const get = (id) => { if (!elements.has(id)) elements.set(id, new Element()); return elements.get(id); };
  let response = {ok: true, status: 200, body: {status: "deleted"}}, release;
  const calls = [];
  const app = vm.createContext({
    Node: Element, Headers,
    document: {getElementById: get, createElement: (tag) => new Element(tag), querySelectorAll: () => all.filter((e) => e.dataset.taskAction)},
    window: {confirm: () => { throw new Error("task actions must not prompt"); }},
    setTimeout: () => 1, clearTimeout() {},
    fetch: async (url, options) => {
      calls.push({url, options});
      await new Promise((resolve) => { release = resolve; });
      return {ok: response.ok, status: response.status, json: async () => response.body};
    }
  });
  const source = fs.readFileSync(path.join(__dirname, "../admin/static/app.js"), "utf8");
  const boundary = source.indexOf("  const preferences =");
  vm.runInContext(source.slice(0, boundary) + 'async function loadPage(){globalThis.reloads++;if(globalThis.holdReload)await new Promise(resolve=>{globalThis.releaseReload=resolve;});} globalThis.reloads=0; globalThis.render=tasksTable;globalThis.details=taskDetails;globalThis.words=state.words;state.csrf="test-csrf";})();', app);
  Object.assign(app.words, JSON.parse(fs.readFileSync(path.join(__dirname, "../admin/static/locales/en.json"), "utf8")));
  const rows = [
    {task_id:"active", status:"running", can_cancel:true},
    {task_id:"complete", status:"completed", can_delete:true},
    {task_id:"failed", status:"failed", can_delete:true, can_retry:true, upload_files:[{status:"failed", path:"/file"}]},
    {task_id:"cancelled", status:"cancelled", can_delete:true},
    {task_id:"cleaning", status:"cancelling", can_cancel:false}
  ];
  const buttons = (element) => element.children.flatMap((child) => child.tag === "button" ? [child] : buttons(child));
  const list = new Element();
  app.render(list, rows);
  const rendered = buttons(list);
  assert.equal(rendered.length, rows.length * 2);
  assert.deepEqual(rendered.map((e) => e.textContent), ["Details","Cancel","Details","Delete","Details","Delete","Details","Delete","Details","Cancelling"]);
  assert.equal(rendered.at(-1).disabled, true);
  app.details(rows[1]);
  const deleting = rendered[3];
  const first = deleting.listeners.click();
  assert.equal(deleting.disabled, true);
  assert.equal(deleting.textContent, "Deleting");
  await deleting.listeners.click();
  assert.equal(calls.length, 1, "repeated click started another deletion");
  // A refresh during the request must retain the disabled action.
  const refreshed = new Element();
  app.render(refreshed, [rows[1]]);
  assert.equal(buttons(refreshed)[1].disabled, true);
  assert.equal(calls[0].url, "/admin/api/v1/tasks/complete");
  assert.equal(calls[0].options.method, "DELETE");
  assert.equal(calls[0].options.headers.get("X-CSRF-Token"), "test-csrf");
  app.holdReload = true;
  release();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(app.reloads, 1);
  assert.equal(deleting.disabled, true, "action re-enabled before the refreshed list arrived");
  assert.equal(buttons(refreshed).length, 0, "successful deletion waited for refresh to remove its row");
  await deleting.listeners.click();
  assert.equal(calls.length, 1, "click during refresh repeated the completed mutation");
  app.holdReload = false;
  app.releaseReload();
  await first;
  assert.equal(get("task-dialog").open, false);
  assert.equal(get("toast").textContent, "Record deleted");
  assert.equal(app.reloads, 1);
  assert.equal(buttons(refreshed).length, 0);
  response = {ok:true, status:200, body:{status:"cancelling"}};
  const cancelling = rendered[1].listeners.click();
  assert.equal(calls[1].url, "/admin/api/v1/tasks/active/cancel");
  assert.equal(calls[1].options.method, "POST");
  release(); await cancelling;
  assert.equal(get("toast").textContent, "Cancelling");
  response = {ok:false, status:409, body:{error:"task_delete_failed"}};
  const failed = rendered[5].listeners.click();
  release(); await failed;
  assert.equal(get("toast").textContent, app.words.task_delete_failed);
  assert.equal(rendered[5].disabled, false);
  assert.equal(rendered[5].textContent, "Delete");
  assert.equal(app.reloads, 3);
});

test("task refresh keeps the server's valid page after deleting the last row", async () => {
  class Element {
    constructor() { this.children = []; this.dataset = {}; }
    append(...children) { this.children.push(...children); }
    replaceChildren(...children) { this.children = children; }
    addEventListener() {}
    setAttribute() {}
    removeAttribute() {}
  }
  const elements = new Map();
  const get = (id) => {
    if (!elements.has(id)) elements.set(id, new Element());
    return elements.get(id);
  };
  get("task-search").value = "";
  get("task-status").value = "";
  const urls = [];
  const app = vm.createContext({
    Node: Element, Headers, AbortController, URLSearchParams,
    location: {hash: "#tasks"},
    document: {getElementById: get, createElement: () => new Element(), querySelectorAll: () => []},
    setTimeout: () => 1, clearTimeout() {},
    fetch: async (url) => {
      urls.push(url);
      return {ok: true, json: async () => ({tasks: [], total: 100, offset: 0, has_active_tasks: false})};
    }
  });
  const source = fs.readFileSync(path.join(__dirname, "../admin/static/app.js"), "utf8");
  const prefix = source.slice(0, source.indexOf("  const preferences ="));
  const load = source.slice(source.indexOf("  async function loadPage()"), source.indexOf("  function stopRouteButton"));
  vm.runInContext(prefix + 'const preferences={close(){}};function refreshTasks(){}state.authenticated=true;state.metadata=true;state.taskOffset=100;' + load + 'globalThis.load=loadPage;})();', app);
  await app.load();
  await app.load();
  assert.equal(new URL(urls[0], "http://console.test").searchParams.get("offset"), "100");
  assert.equal(new URL(urls[1], "http://console.test").searchParams.get("offset"), "0");
});
