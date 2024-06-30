# Java

> Weblogic 等服务对换行很敏感， 如果你需要对文件进行修改，请务必确保去除了不必要的换行，
> 尤其是文件结束的换行，否则可能无法使用

运行环境: JDK 4~21

| 文件                       | 全双工 | 半双工 | 负载转发 | 备注                                                                     |
|:-------------------------|:---:|:---:|:----:|:-----------------------------------------------------------------------|
| `suo5.jsp`               |  ✓  |  ✓  |  ✓   |                                                                        |
| `suo5.jspx`              |  ✓  |  ✓  |  ✓   |                                                                        |
| `Suo5Filter.java`        |  ✓  |  ✓  |  ✓   | `javax.servlet.Filter` 的实现，用于经典中间件 `Filter` 类型的内存马注入                   |
| `Suo5WebFluxFilter.java` |  ✓  |  ✓  |  x   | `org.springframework.web.server.WebFilter` 的实现， 用于响应式的 Spring Netty 环境 |
| `Suo5WebFluxSpEL.txt`    |  ✓  |  ✓  |  x   | Spring Cloud Gateway `CVE-2022-22947` 的一键注入 Suo5 的 Payload             |

> WebFlux 的负载转发功能时可以支持的，时间比较仓库还没写，后面会更新

内存马注入推荐参考这个项目，其支持生成各种中间件的一键 Suo5
注入逻辑 [java-memshell-generator-release](https://github.com/pen4uin/java-memshell-generator-release)
