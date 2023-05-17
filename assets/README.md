# Suo5 Remote Server

> 注意：suo5.jsp 请勿放到编辑器格式化之类的，否则在 weblogic 等服务中可能会无法使用，主要是换行导致的。

实战中推荐使用内存马的方式来加载, jsp 的方式容易被安全设备检测到。

- `suo5.jsp` servlet 的实现
- `Suo5Filter.java` 一个简易的 Filter 实现，可以改造后用于 Filter 型内存马注入

如果想要其他版本的，可以利用 git 的 release tag 进入。

## 测试通过的中间件

详情见 [Github Action Tests](https://github.com/zema1/suo5/actions/workflows/test.yml?query=branch%3Amain)

- Tomcat 4,5,6,7,8,9,10
- Weblogic 10,12,14
- Jboss 4,6
- Jetty 9,10,11
- WebSphere 8,9,22,23