# Node.js

环境依赖：Node.js 18+

| 运行环境                    | 自动检测 | 全双工 | 半双工 | 短链接 |
|:------------------------|:----:|:---:|:---:|:---:|
| Node.js Standalone (18/22) |  ✓   |  ✓  |  ✓  |  ✓  |

## 独立运行

`suo5.js` 不依赖第三方 npm 模块，直接运行即可：

```bash
node suo5.js
```

默认监听 `8080` 端口，Suo5 客户端连接地址为：

```bash
./suo5 -t http://target:8080/
```

## Next.js 内存马

`next_payload.http` 是针对 CVE-2025-55182/CVE-2025-66478 生成的请求。根据目标修改请求中的
`Host` 和请求路径后发送，执行成功后会在目标现有的 Node.js HTTP Server 中挂载 `/test` 路由。

Suo5 客户端连接地址为：

```bash
./suo5 -t http://target:3000/test
```
