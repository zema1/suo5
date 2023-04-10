# 更新记录

## [0.6.0] - 2023-04-10

### 新增

- 降低 JDK 依赖到 Java 4, 目前兼容 Java 4 ~ Java 19 全版本
- 新增 Tomcat Weblogic Resin Jetty 等中间件的自动化测试, 下列版本均测试通过:
    - Tomcat 4,5,6,7,8,9,10
    - Weblogic 10,12,14
    - Jboss 4,6
    - Jetty 9,10,11
- 更换一个更圆润的图标, 感谢 [@savior-only](https://github.com/savior-only)

## 修复

- 修复 GUI 版本在高版本 Edge 下启动缓慢的问题

## [0.5.0] - 2023-03-14

### 新增

- 每 5s 发送一个心跳包避免远端因 `ReadTimeout` 而关闭连接 [#12](https://github.com/zema1/suo5/issues/12)
- 改进地址检查方式，负载均衡的转发判断会更快一点

## [0.4.0] - 2023-03-05

### 新增

- 支持在负载均衡场景使用，需要通过 `-r` 指定一个 url，流量将集中到这个 url
- 支持自定义 header，而不仅仅是自定义 User-Agent [#5](https://github.com/zema1/suo5/issues/6)
- 优化连接控制，本地连接关闭后能更快的释放底层连接

### 修复

- 修复命令行版设置认证信息不生效的问题  [#5](https://github.com/zema1/suo5/issues/8)

## [0.3.0] - 2023-02-24

### 新增

- 支持自定义连接方法，如 GET, PATCH
- 支持配置上游代理, 仅支持 socks5
- 增加英文文档和变更记录

### 修复

- 修复部分英文语法错误

## [0.2.0] - 2023-02-22

### 新增

- 发布第一版本，包含 GUI 和 命令行版
- 使用 Github Action 自动构建所有版本的应用