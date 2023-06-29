# Suo5 Remote Server

> 注意：suo5.jsp 请勿放到编辑器格式化之类的，否则在 weblogic 等服务中可能会无法使用，主要是换行导致的。


- `suo5.jsp`
- `suo5.jspx`
- `Suo5Filter.java` 一个简易的 Filter 实现，可以改造后用于 Filter 型内存马注入

实战中推荐使用内存马的方式来加载, 其次是 jspx，再然后是 jsp。 深度使用的同学建议自行修改部分特征以免流量被识别，在功能做完善之前安全对抗不是这个项目的发力点。


## 测试通过的中间件

详情见 [Github Action Tests](https://github.com/zema1/suo5/actions/workflows/test.yml?query=branch%3Amain)

- Tomcat 4,5,6,7,8,9,10
- Weblogic 10,12,14
- Jboss 4,6
- Jetty 9,10,11
- WebSphere 8,9,22,23
- Resin 4

## 为何显示连接成功但无法使用？

首先请确保使用的是最新版本，如果你遇到的环境是 `泛微OA(resin)`、`Jira(tomcat)` 等，请尝试使用内存马的版本，很多时候 jsp(x) 不行但是内存马是可以的。

从根本上讲，有部分情况 `suo5` 是无法支持的，这并非是程序 bug，而是工作原理使然，`suo5` 要求目标的响应是流式的，如果目标中间件或是负载均衡对响应有缓存，这种只能使用传统代理来构建隧道了。