# 测试方法

## 准备靶站

进入 `nginx-tomcat`目录，复制需要测试的 `jsp` 文件到 code 文件夹，这里对比测试的两个是:

- `suo5.jsp` 本项目，版本 `v0.2.0`
- `neo.jsp` [Neo-reGeorg](https://github.com/L-codes/Neo-reGeorg)， 版本`v5.0.0`

运行 `docker-compose up -d` 将启动测试靶站，为了模拟实际场景, `:8070` 端口是 nginx+tomcat 反代的服务。
在这个例子中，其路径分别为：

```
http://192.168.198.130:8070/tomcat/code/suo5.jsp
http://192.168.198.130:8070/tomcat/code/neo.jsp
```

连接成功后，配置 `proxifier` 规则使 `ssh` 走上述工具创建的 http 代理。

## 小文件测试

在本机使用下列命令创建 100 个小文件，然后使用 scp 测试传输这 100 个文件所需的时间。

```
for i in {1..100}; do echo $RANDOM > $RANDOM.txt; done;
```

+ suo5.jsp **5.21s**
+ neo.jsp 64.53s

![little-files.png](img/little-files.png)

## 大文件测试

在本机使用下列命令创建 1 个 100MB 的文件，然后使用 scp 测试传输这个文件所需的时间

```
dd if=/dev/urandom of=100mb.bin bs=1024 count=102400
```

+ suo5.jsp **8.78s**
+ neo.jsp 20.14s

![big-files.jpg](img/big-files.png)

值得一提的是，测试机器的带宽是 100MB 的网络，所以 `suo5` 实际上已经跑满带宽了。

如果去掉这个带宽限制，那么结果会变成这样:

+ suo5.jsp **0.91s**
+ neo.jsp 20.42s

![big-files.jpg](img/big-files.png)

## 结论

在小文件比较多时, `suo5` 的传输效率 `Neo-reGeorg` 的12倍；当传输大文件时，`suo5` 理论上可以跑满带宽，在 100MB 的带宽下，其效率是 `Neo-reGeorg` 的 2.5 倍。

