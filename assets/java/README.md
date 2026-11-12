# Java

推荐使用 [MemshellParty](https://github.com/ReaJason/MemShellParty) 项目来生成内存马并进行注入。

环境依赖：Java 6及以上版本。

下方表格是 CI 测试中确认可以使用的版本，实际上绝大部分中间件都可以使用，只要实现了 Java Servlet 规范即可。

| 中间件                  | 自动检测 | 全双工 | 半双工 | 短链接 |
|:---------------------|:----:|:---:|:---:|:---:|
| Tomcat (4.1-10)      |  ✓   |  ✓  |  ✓  |  ✓  |
| Jetty (9.4-11.0)     |  ✓   |  ✓  |  ✓  |  ✓  |
| Resin (3.1-4.0)      |  ✓   |  ✓  |  ✓  |  ✓  |
| JBoss (4-7, EAP-7)   |  ✓   |  ✓  |  ✓  |  ✓  |
| WebSphere            |  ✓   |  ✓  |  ✓  |  ✓  |
| Weblogic (10.3-14.1) |  ✓   |  ✓  |  ✓  |  ✓  |
| TongWeb (6)          |  ✓   |  ✓  |  ✓  |  ✓  |
| Netty (4.x)          |  ✓   |  ✓  |  ✓  |  ✓  |
| Spring Webflux       |  ✓   |  ✓  |  ✓  |  ✓  |

Netty 使用单独的 handler 实现，见 [Suo5NettyHandler.java](Suo5NettyHandler.java)，挂载在 `HttpServerCodec` 之后、`HttpObjectAggregator` 之前。

Spring Webflux 使用 [Suo5WebFluxFilter.java](Suo5WebFluxFilter.java)。
