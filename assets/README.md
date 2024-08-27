# Suo5 Remote Scripts

## 必读

#### 为何显示连接成功但无法使用

1. 检查客户端与服务端使用的是否是最新版
2. 尝试使用 `GET`/`PUT` 等方法而不是 `POST` 来连接
3. 尝试禁用 `cookiejar` 或 `gzip`，部分奇葩环境禁用后可以正常连接
4. 提交 issue，说明目标环境（系统、运行环境、中间件）等信息

从根本上讲，有部分情况 `suo5` 是无法支持的，这并非是程序 bug，而是工作原理使然。
`suo5` 要求目标能够支持流式响应，如果目标中间件或是负载均衡对响应有缓存，这种情况下 `suo5` 是暂不支持的。

明确已知下列情况不可用：

- 目标存在两层反代, 比如一层是 CDN，一层是 Nginx，无法使用
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

## 进入查看

- [Java](java)
- [.Net](.net)
- [PHP](php)

## 测试通过的服务

详情见 [Github Action Tests](https://github.com/zema1/suo5/actions/workflows/test.yml?query=branch%3Amain)

- Tomcat 4,5,6,7,8,9,10
- Weblogic 10,12,14
- Jboss 4,6
- Jetty 9,10,11
- WebSphere 8,9,22,23
- Resin 4
- IIS 7+
