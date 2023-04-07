# Suo5 Remote Server

> 注意：suo5.jsp 请勿放到编辑器格式化之类的，否则在 weblogic 等服务中可能会无法使用，因为会破坏文件格式，主要是换行导致的。

实战中推荐使用内存马的方式来加载, suo5.jsp 的方式可能会被安全设备检测到。

- `suo5.jsp` servlet 的实现
- `Suo5Filter.java` 一个简易的 Filter 实现，可以用于 Filter 内存马注入

如果想要其他版本的，可以利用 git 的 release tag 进入。