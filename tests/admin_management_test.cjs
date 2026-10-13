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
