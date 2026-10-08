# SaveAnyBot Plugins

SaveAnyBot 可通过插件扩展功能, 目前仅支持 Parser (解析器)插件.

## Parser

解析器为 SaveAnyBot 提供了处理非 Telegram 文件的能力, 例如下载其他网站的图片或视频.

当前解析器接口定义如下:

```go
type Parser interface {
	CanHandle(url string) bool // 判断是否能处理给定的 URL
	Parse(ctx context.Context, url string) (*Item, error) // 解析 URL, 返回 Item
}

// Resource is a single downloadable resource with metadata.
type Resource struct {
	URL       string            `json:"url"`
	Filename  string            `json:"filename"` // with ext
	MimeType  string            `json:"mime_type"`
	Extension string            `json:"extension"`
	Size      int64             `json:"size"`    // 0 when unknown
	Hash      map[string]string `json:"hash"`    // {"md5": "...", "sha256": "..."}
	Headers   map[string]string `json:"headers"` // HTTP headers when downloading
	Extra     map[string]any    `json:"extra"`
}

type Item struct {
	Site        string         `json:"site"`
	URL         string         `json:"url"` // original URL of the item
	Title       string         `json:"title"`
	Author      string         `json:"author"`
	Description string         `json:"description"`
	Tags        []string       `json:"tags"`
	Resources   []Resource     `json:"resources"`
	Extra       map[string]any `json:"extra"`
}
```

### Write a Parser Plugin

解析器插件可使用 JavaScript 编写, SaveAnyBot 使用 [goja](https://github.com/dop251/goja) 提供运行时, 并向其中注入了以下全局函数或对象:

- **registerParser**: 用于注册解析器, 每个插件必须调用此函数以注册
- **console.log**: 调用 go 端的 logger 打印日志
- **ghttp**: 提供 HTTP 请求功能
- **playwright**: 提供基于 Playwright 的浏览器自动化请求功能

插件需要提供元数据 `metadata` 并实现 `canHandle` 和 `parse` 两个函数, 最后调用 `registerParser` 注册解析器.

同一脚本注册的所有解析器（包括运行时通过保存的 `registerParser` 引用注册的解析器）共享串行执行的 VM；不同脚本可并行。等待 VM 和纯 JS 执行都响应调用方取消：初始化上限 30 秒、`canHandle` 上限 5 秒、`parse` 上限 2 分钟，均包括等待时间，更短的请求期限优先。取消会中断纯 JS 循环，并在下一次调用前清除中断状态；`ghttp` 使用本次调用的 context。

这些期限不意味着 Go 原生调用可以被强制停止。Playwright 的安装等待和各阶段之间会检查取消，浏览器启动与导航超时取当前调用的剩余期限；已经开始的安装、驱动启动、页面操作和关闭仍可能占用 VM 直到返回。请让 `canHandle` 只做快速 URL 判断，浏览器请求放在 `parse` 中。JS 函数参数和 `Item/Resource` 返回结构保持不变。

安装或重新加载同一来源文件时，校验成功后整体替换该文件注册的解析器，保持相对其他来源的匹配顺序；不同目录的同名文件是不同来源。安装失败保留原文件与运行实例。替换不会中止已经开始的旧调用，但旧 VM 不再接收新调用，等待者返回替换错误；旧调用保存的 `registerParser` 引用不能再向当前注册表追加解析器。运行时注册在未被替换的脚本中仍然可用。VM 没有常驻解析 goroutine，旧实例在已开始调用和旧引用释放后可回收。

#### Plugin Metadata

插件元数据是一个 JavaScript 对象:

```js
const metadata = {
    version: "1.0.0", // 插件兼容版本号, 必须提供, 其他字段可选
    name: "Example Parser", // 插件名称
    description: "A parser for example links", // 插件描述
    author: "Krau", // 插件作者
}
```

#### canHandle Function

`canHandle`: `canHandle(url: string): boolean` , 用于判断当前解析器能否解析给定的 URL, 返回布尔值, 例如:

```js
const canHandle = function (url) {
	return url.includes("youtube.com/watch?v");
};
```

这将让 SaveAnyBot 在遇到包含 `youtube.com/watch?v` 的 url 时调用当前解析器的 `parse`.

#### parse Function

`parse`: `parse(url: string): Item` , 是核心解析函数, 用于解析给定的 url, 返回一个 `Item` 对象, 例:

```js
const parse = function (url) {
    var result = {
        // 元信息
        site: "YouTube",
        url: url,
        title: "测试 YouTube 视频",
        author: "某视频作者",
        description: "这是一个测试视频",
        tags: ["test", "youtube"],
        // 资源(可下载的文件)列表
        resources: [
            {
                url: "https://example.com/video1.mp4", // 文件直链
                filename: "somevideo.mp4", // 文件名
                mime_type: "video/mp4", // 文件 MIME 类型, 可选
                extension: "mp4", // 文件扩展名, 可选
                size: 100 * 1024 * 1024, // 文件大小, 单位为字节, 未知可以设置为 0
                hash: {}, // 文件哈希, 可选, 格式为 {"md5": "xxx", "sha256": "xxx"} 等
                headers: {}, // 下载文件时所需的 HTTP 头部, 可选, 例如 {"User-Agent": "Mozilla/5.0"}
                extra: {} // 额外信息, 可选, 可以包含任何自定义数据
            },
            {
                url: "https://example.com/picture1.png",
                filename: "picture1.png",
                mime_type: "image/png",
                extension: "png",
                size: 1 * 1024 * 1024,
                hash: {},
                headers: {},
                extra: {}
            }
        ],
        extra: {}
    };
    return result;
}
```

#### HTTP Requests

使用 `ghttp` 对象以发起 HTTP 请求.

**ghttp.get(url: string)** 发起 GET 请求, 当成功时返回响应体字符串, 失败时或响应状态码不为 200 时返回一个包含 `error` 字段的对象:

```js
const response = ghttp.get("https://example.com/someapi");
if (response.error) {
	console.log("Request failed:", response.error);
}
if (response.status) {
	console.log("Response status:", response.status);
}
```

**ghttp.getJSON(url: string)** 发起 GET 请求并将响应体解析为 JSON 对象, 始终返回以下对象:

```js
{
	data?: any, // 当请求成功且响应体为合法 JSON 时包含解析后的数据
	error?: string, // 当请求失败或响应状态码不为 200 时包含错误信息
	status?: number, // 响应状态码, 仅当响应状态码不为 200 时包含
}
```

#### Playwright

使用 `playwright` 对象以发起基于浏览器的请求.

**playwright.get(url: string)** 导航到给定页面，等待网络空闲后返回页面 HTML 字符串；导航、读取或关闭失败，以及最终响应状态码大于等于 400 时，返回包含 `error` 字段的对象。保留接受其他成功状态及重定向的既有行为：

所有插件在同一进程内共用 `./playwright` 的安装协调，成功安装后复用安装结果，失败可由后续请求重试；每次请求仍创建独立驱动和浏览器。等待其他请求安装时可以取消，但已经开始的驱动/浏览器安装不支持强制取消；首次下载应预留时间。浏览器启动最多 30 秒，导航最多 60 秒，两者都以本次调用更短的剩余期限为准；该设置依赖浏览器驱动处理超时，不是整个 `get` 的硬期限。各阶段返回后再检查取消，取消后的结果不会交给后续任务。退出时先关闭浏览器再停止驱动，即使浏览器关闭报错也会尝试停止驱动，关闭错误会记录日志并返回错误对象。驱动启动和关闭仍需真实运行验收。协调不覆盖其他进程；外部进程应使用独立工作目录。

```js
const response = playwright.get("https://example.com/somepage");
if (response.error) {
    console.log("Request failed:", response.error);
}
```

---

最后别忘了调用 `registerParser` 注册解析器:

```js
registerParser({
	metadata,
	canHandle,
	parse
});
```

### Examples

请先查看 [example_parser_basic.js](./example_parser_basic.js) 了解最简示例解析器插件的实现.

然后查看 [example_parser_danbooru.js](./example_parser_danbooru.js) , 这是一个可直接使用的插件, 用于解析 Danbooru 图片页面并提取图片资源.
