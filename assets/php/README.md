# PHP

运行环境: PHP5.6 ~ 8.2 + Nginx

> PHP 的支持仍然是实验性质的，在你想使用之前，务必认真阅读这个文档

在 Suo5 当前的通信模型下， 本地的每个 `socks5` 连接都需要与远程构建出一条下行的无缓存的长连接才可以。
在其他语言中，这并不是什么大问题，最多只是会占用一个处理线程，但是在 PHP 就不一样了。 实践中通常使用 FastCGI 的形式来运行
PHP，Suo5 的通信模式会导致每个连接都占用一个 PHP-CGI 的 Worker 不释放，直到连接关闭才释放，
在 Worker 被占用期间是无法再去执行其它的 PHP 脚本的，一旦 Worker 被占满，日志中就会观察到下面这个这个错误，这时无论是 Suo5 还是网站正常请求都无法访问了。

```log
WARNING: [pool www] server reached pm.max_children setting (5), consider raising it
```

**这就意味着，如果目标配置的 Worker 数量不多，同时 Suo5 连接数较多，会导致目标阻塞、失去响应直接连接超时释放才会恢复,
因此在使用之前你务必检查一下目标的 Worker 数量是否足够再决定是否使用。**

## Nginx + PHP

这是我本地开发 Suo5 时使用的环境，也是在 CI 中大量测试过的环境，基本上不存在无法使用的问题，但是绕不开上面说的 Worker 数量问题。

比较遗憾的是，默认的 Worker 数量是一个很小的值 `5`，这个值由 php fpm 的配置文件中的 `pm.max_children` 项决定，比如
`/etc/php/7.4/fpm/pool.d/www.conf`中的：

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

这基本就是不可用的状态，前4个连接都会很丝滑，但是第5个连接就会卡住，这种就不要用了。

## Apache + PHP

暂没有找到支持的办法，主要原因在于 Apache 默认会对响应进行缓存，导致 Suo5 的通信模式无法正常工作，我也没有找到能够绕过这个限制的方法，
因此如果你的机器运行的是 Apache，就没必要尝试了。
