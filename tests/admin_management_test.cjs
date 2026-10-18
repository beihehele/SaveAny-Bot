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
    constructor() { this.children = []; this.valueText = ""; }
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
