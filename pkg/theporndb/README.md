# theporndb

ThePornDB REST 查询客户端，仅依赖 Go 标准库。按官方 OpenAPI v3.24.769
实现 GET 接口；不包含创建、评分、收藏等写入接口。

协议：https://api.theporndb.net/docs

```go
import (
    "context"
    "net/url"
    "os"

    "github.com/lsongdev/files-go/pkg/theporndb"
)

client, err := theporndb.New(theporndb.Options{
    Token: os.Getenv("THEPORNDB_TOKEN"),
})
if err != nil {
    return err
}
page, err := client.SearchScenes(context.Background(), url.Values{
    "q":        {"search terms"},
    "page":     {"1"},
    "per_page": {"10"},
})
if err != nil {
    return err
}
for _, scene := range page.Data {
    detail, err := client.GetScene(context.Background(), scene.ID, nil)
    if err != nil {
        return err
    }
    _ = detail.Data
}
```

所有方法接收 context；查询参数使用 `url.Values`，按协议原名传入，
支持重复参数。无需查询参数时传 `nil`。分页由调用方通过 `page` 控制，
结果包含 `Data`、`Meta` 和 `Links`。

提供 scenes、movies、JAV 的搜索、详情、Hash 匹配、相似内容，以及
演员、站点、标签、导演、变更、用户等查询。UUID 和 slug 均以字符串传入。
详情接口的 `add_to_collection=true` 会修改服务端收藏状态，仅在需要时显式传入。

`Options.HTTPClient` 可注入代理或自定义传输；默认超时 30 秒。
`Options.BaseURL` 可指定测试服务地址。客户端不会自动重试；非 2xx
响应返回 `*theporndb.Error`，可用 `errors.As` 取得 `StatusCode`、
`Message`、`Body`（最多 64 KiB）和 `RetryAfter`。

可通过 `Options.Cache` 注入缓存，实现与 TMDB 相同的接口：

```go
type Cache interface {
    Get(ctx context.Context, key string) ([]byte, bool)
    Set(ctx context.Context, key string, value []byte)
}
```

客户端缓存成功解码的 API 响应（包含空搜索结果）和 JSON 404 响应。
搜索、详情及分页均自动使用缓存，过期策略由缓存实现控制。
不配置 Cache 时直接请求 API。缓存键区分服务器、账号、路径和查询参数，
Token 仅用于生成哈希，不以明文写入缓存键。
用户信息、评分、收藏、`/user/*` 和含 `add_to_collection` 参数的请求跳过缓存。
认证错误、限流、服务端错误、读取失败与无效响应不缓存。

files-go 使用 SQLite 适配器 `media.ThePornDBCache`，有效期 30 天，
数据库仍位于配置的 data 目录。JAV 识别层不再单独缓存匹配结果。

Token 仅由调用者提供，不在源码中存储。运行单元测试：

```sh
go test ./pkg/theporndb
```

设置 `THEPORNDB_TOKEN` 后可运行真实搜索和详情验证：

```sh
go test ./pkg/theporndb -run TestLive -v
```
