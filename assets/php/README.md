# PHP

环境依赖：PHP 5.6+

下方表格是 CI 测试中确认可以使用的版本和模式：

| 运行环境                 | 自动检测 | 全双工 | 半双工 | 短链接 |
|:---------------------|:----:|:---:|:---:|:---:|
| PHP-Nginx (5.6-8.4)  |  ✓   |  ✗  |  ✓  |  ✓  |
| PHP-Apache (5.6-8.4) |  ✓   |  ✗  |  ✗  |  ✓  |

> Apache 环境下由于无法构建流式下行通道，暂没有办法支持半双工

## 使用必读

我们平常遇到的 PHP 大多是以 FastCGI 的形式来运行 Web 服务的，比如 Nginx + PHP-FPM，Apache + mod_fcgid 等等。
FastCGI 有个问题是脚本执行结束时会自动进行资源清理，包括脚本执行过程中创建的 Socket。但是对于代理而言，远程 Socket
是不应该被强行关闭的。
为了解决这个问题，Suo5 通信时每个连接都占用一个 Worker 不释放，直到连接关闭才释放， 在 Worker 被占用期间是无法再去执行其它的
PHP 脚本的，
**这就意味着，如果目标配置的 Worker 数量不多，同时 Suo5 连接数较多，会导致目标阻塞、失去响应直接连接超时释放才会恢复,
因此在使用之前你务必检查一下目标的 Worker 数量是否足够再决定是否使用。**

当 worker 被占满时，PHP-FPM 日志中会有类似如下的警告出现, 此时无论是 Suo5 还是正常的请求都会被阻塞:

```log
WARNING: [pool www] server reached pm.max_children setting (5), consider raising it
```

这个值由 php fpm 的配置文件中的 `pm.max_children` 项决定，比如 `/etc/php/7.4/fpm/pool.d/www.conf`中的：

```ini
; The number of child processes to be created when pm is set to 'static' and the
; maximum number of child processes when pm is set to 'dynamic' or 'ondemand'.
; This value sets the limit on the number of simultaneous requests that will be
; served. Equivalent to the ApacheMaxClients directive with mpm_prefork.
; Equivalent to the PHP_FCGI_CHILDREN environment variable in the original PHP
; CGI. The below defaults are based on a server without much resources. Don't
; forget to tweak pm.* to fit your needs.
; Note: Used when pm is set to 'static', 'dynamic' or 'ondemand'
; Note: This value is mandatory.
pm = dynamic
pm.max_children = 5
```

对于这类比较小的，基本就是不可用的状态，前4个连接都会很丝滑，但是第5个连接就会卡住，这种就不要用了。当然，你也可以尝试手动修改配置，增加一下
`pm.max_children` 的值，然后重启 PHP-FPM 服务。
