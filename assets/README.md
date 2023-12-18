# Suo5 Remote Scripts

## 必读

#### 为何显示连接成功但无法使用

1. 检查客户端与服务端使用的是否是最新版
2. 尝试使用 `GET`/`PUT` 等方法而不是 `POST` 来连接
3. 尝试禁用 `cookiejar` 或 `gzip`
4. 提交 issue，说明目标环境（系统、运行环境、中间件）等信息

从根本上讲，有部分情况 `suo5` 是无法支持的，这并非是程序 bug，而是工作原理使然。
`suo5` 要求目标能够支持流式响应，如果目标中间件或是负载均衡对响应有缓存，这种只能使用传统方式来构建隧道了。

明确已知下列服务不可用：

- `泛微OA(resin)`、`Jira(tomcat)` 请使用内存马的版本，`jsp(x)` 无法使用
- `Kong` 无法使用

#### 如何设置密码使脚本只能自己连接

Suo5 暂时不具备设置密码的功能，短期也不会加。但是目前脚本中有判断 `User-Agent` 的逻辑，即限定了只能下面这个 `User-Agent`
才能连接,
你可以把脚本里改成别的，然后连接时指定对应的 `User-Agent` 即可（命令行 `--ua`，界面版在高级设置里）

```
User-Agent: Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.1.2.3
```

#### 如果这个项目对你帮助很大

可以考虑请我 [喝杯咖啡](../DONATION.md)，作者一直在用爱发电，你的支持会让他更有动力！

## Java

> Weblogic 等服务对换行很敏感， 如果你需要对文件进行修改，请务必确保去除了不必要的换行，
> 尤其是文件结束的换行，否则可能无法使用

运行环境: JDK 4~21

| 文件                       | 全双工 | 半双工 | 负载转发 | 备注                                                                     |
|:-------------------------|:---:|:---:|:----:|:-----------------------------------------------------------------------|
| `suo5.jsp`               |  ✓  |  ✓  |  ✓   |                                                                        |
| `suo5.jspx`              |  ✓  |  ✓  |  ✓   |                                                                        |
| `Suo5Filter.java`        |  ✓  |  ✓  |  ✓   | `javax.servlet.Filter` 的实现，用于经典中间件 `Filter` 类型的内存马注入                   |
| `Suo5WebFlexFilter.java` |  ✓  |  ✓  |  x   | `org.springframework.web.server.WebFilter` 的实现， 用于响应式的 Spring Netty 环境 |
| `Suo5WebFlexSpEL.txt`    |  ✓  |  ✓  |  x   | Spring Cloud Gateway `CVE-2022-22947` 的一键注入 Suo5 的 Payload             |

> WebFlex 的负载转发功能时可以支持的，时间比较仓库还没写，后面会更新

内存马注入推荐参考这个项目，其支持生成各种中间件的一键 Suo5
注入逻辑 [java-memshell-generator-release](https://github.com/pen4uin/java-memshell-generator-release)

## .Net

运行环境: .Net Framework >= 2.0; .NetCore; .Net

| 文件          | 全双工 | 半双工 | 负载转发 | 备注 |
|:------------|:---:|:---:|:----:|:---|
| `suo5.aspx` |  x  |  ✓  |  ✓   |    |

1. `.Net` 全双工的实现主要卡在了流式的请求传输上，我发现在 `.Net` 中必须等到请求的 `Body` 结束才能在 aspx 脚本内拿到
   `Request` 对象，这就导致了无法在请求过程中进行响应，因此只能使用半双工的方式来实现。
   如通过你有思路突破这个限制，欢迎与我讨论
2. 脚本中有一个对服务线程池调整的逻辑，至少会调整为 256，如果并发数超过这个数量，请求会变得很慢，这和 IIS 的请求模型有关。
   如果你需要更大的并发，需要把这个值改大一些。

## PHP

不打算做了，除非你说服我这个真的很重要 (

## 测试通过的服务

详情见 [Github Action Tests](https://github.com/zema1/suo5/actions/workflows/test.yml?query=branch%3Amain)

- Tomcat 4,5,6,7,8,9,10
- Weblogic 10,12,14
- Jboss 4,6
- Jetty 9,10,11
- WebSphere 8,9,22,23
- Resin 4
- IIS 7+
