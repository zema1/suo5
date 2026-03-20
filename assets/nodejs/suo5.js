const http = require('http');
const https = require('https');
const net = require('net');
const os = require('os');

const ctx = new Map();
const addrs = collectAddr();
const CHARACTERS = "abcdefghijklmnopqrstuvwxyz0123456789";

const server = http.createServer(async (req, res) => {
    try {
        await processRequest(req, res);
    } catch (e) {
        if (!res.headersSent) res.writeHead(500);
        res.end();
    }
});

async function processRequest(req, res) {
    let reader = new StreamReader(req);
    let dataMap;
    try {
        dataMap = await unmarshalBase64(reader);
    } catch (e) {
        res.end();
        return;
    }

    if (!dataMap || dataMap.size === 0) {
        res.end();
        return;
    }

    let modeData = dataMap.get("m");
    let actionData = dataMap.get("ac");
    let tunIdData = dataMap.get("id");
    let sidData = dataMap.get("sid");

    if (!actionData || actionData.length !== 1 || !tunIdData || tunIdData.length === 0 || !modeData || modeData.length === 0) {
        res.end();
        return;
    }

    let sid = sidData ? sidData.toString() : null;
    let tunId = tunIdData.toString();
    let mode = modeData[0];

    switch (mode) {
        case 0x00: // Handshake
            sid = randomString(16);
            await processHandshake(req, res, dataMap, tunId, sid);
            break;
        case 0x01: // Full Stream
            setBypassHeader(res);
            await processFullStream(reader, res, dataMap, tunId);
            break;
        case 0x02: // Half Stream
            setBypassHeader(res);
        case 0x03: // Classic
            let isRedirected = await handleRedirect(req, res, dataMap, reader);
            if (isRedirected) return;

            if (!sid || !ctx.has(sid)) {
                res.writeHead(403);
                res.end();
                return;
            }

            let dirtySize = ctx.get(sid + "_jk") || 0;

            if (mode === 0x02) {
                // Half Mode
                await writeAndFlush(res, processTemplateStart(res, sid), dirtySize);
                let loopDataMap = dataMap;
                do {
                    await processHalfStream(loopDataMap, res, tunId, dirtySize);
                    try {
                        loopDataMap = await unmarshalBase64(reader);
                        if (loopDataMap.size === 0) break;
                        tunId = loopDataMap.get("id").toString();
                    } catch (e) { break; }
                } while (true);
                await writeAndFlush(res, processTemplateEnd(sid), dirtySize);
                res.end();
            } else {
                // Classic Mode
                let buffers = [];
                buffers.push(processTemplateStart(res, sid));

                let loopDataMap = dataMap;
                do {
                    let cData = await processClassic(req, loopDataMap, tunId);
                    if (cData && cData.length > 0) buffers.push(cData);

                    try {
                        loopDataMap = await unmarshalBase64(reader);
                        if (loopDataMap.size === 0) break;
                        tunId = loopDataMap.get("id").toString();
                    } catch (e) { break; }
                } while (true);

                buffers.push(processTemplateEnd(sid));
                let finalBuf = Buffer.concat(buffers);
                res.setHeader("Content-Length", finalBuf.length);
                await writeAndFlush(res, finalBuf, 0);
                res.end();
            }
            break;
    }
}

async function processHandshake(req, res, dataMap, tunId, sid) {
    let redirectData = dataMap.get("r");
    if (redirectData && redirectData.length > 0 && !isLocalAddr(redirectData.toString())) {
        res.writeHead(403);
        res.end();
        return;
    }

    let tplData = dataMap.get("tpl");
    let ctData = dataMap.get("ct");
    if (tplData && ctData) {
        let tplStr = tplData.toString();
        let parts = tplStr.split("#data#");
        ctx.set(sid, {
            ct: ctData.toString(),
            start: parts[0] || "",
            end: parts[1] || ""
        });
    } else {
        ctx.set(sid, { ct: "", start: "", end: "" });
    }

    let jkData = dataMap.get("jk");
    if (jkData) {
        let dSize = parseInt(jkData.toString(), 10) || 0;
        ctx.set(sid + "_jk", dSize < 0 ? 0 : dSize);
    }

    let isAutoData = dataMap.get("a");
    let isAuto = isAutoData && isAutoData.length > 0 && isAutoData[0] === 0x01;

    if (isAuto) {
        setBypassHeader(res);
        await writeAndFlush(res, processTemplateStart(res, sid), 0);
        await writeAndFlush(res, marshalBase64(newData(tunId, dataMap.get("dt"))), 0);

        await new Promise(r => setTimeout(r, 2000));

        await writeAndFlush(res, marshalBase64(newData(tunId, Buffer.from(sid))), 0);
        await writeAndFlush(res, processTemplateEnd(sid), 0);
        res.end();
    } else {
        let b1 = processTemplateStart(res, sid);
        let b2 = marshalBase64(newData(tunId, dataMap.get("dt")));
        let b3 = marshalBase64(newData(tunId, Buffer.from(sid)));
        let b4 = processTemplateEnd(sid);
        let body = Buffer.concat([b1, b2, b3, b4]);

        res.setHeader('Content-Length', body.length);
        await writeAndFlush(res, body, 0);
        res.end();
    }
}

async function processFullStream(reader, res, dataMap, tunId) {
    let host = dataMap.get("h").toString();
    let port = parseInt(dataMap.get("p").toString());

    let socket = new net.Socket();
    let isSocketAlive = false;

    try {
        await new Promise((resolve, reject) => {
            socket.setTimeout(5000);
            socket.connect(port, host, () => {
                isSocketAlive = true;
                socket.setTimeout(0);
                resolve();
            });
            socket.once('error', reject);
            socket.once('timeout', () => {
                socket.destroy();
                reject(new Error("timeout"));
            });
        });
        await writeAndFlush(res, marshalBase64(newStatus(tunId, 0x00)), 0);
    } catch (e) {
        await writeAndFlush(res, marshalBase64(newStatus(tunId, 0x01)), 0);
        res.end();
        return;
    }

    let readerPromise = (async () => {
        while (true) {
            let nMap;
            try {
                nMap = await unmarshalBase64(reader);
            } catch (e) { break; }

            if (!nMap || nMap.size === 0) break;
            let action = nMap.get("ac")[0];
            if (action === 0x01) {
                let dt = nMap.get("dt");
                if (dt && dt.length > 0) socket.write(dt);
            } else if (action === 0x10) {
                await writeAndFlush(res, marshalBase64(newHeartbeat(tunId)), 0);
            }
        }
    })();

    socket.on('data', async (chunk) => {
        await writeAndFlush(res, marshalBase64(newData(tunId, chunk)), 0);
    });

    await new Promise(resolve => {
        socket.on('close', resolve);
        socket.on('error', resolve);
    });

    await writeAndFlush(res, marshalBase64(newDel(tunId)), 0);
    res.end();
}

async function processHalfStream(dataMap, res, tunId, dirtySize) {
    let action = dataMap.get("ac")[0];
    try {
        switch (action) {
            case 0x00:
                let createData = await performCreate(dataMap, tunId, true);
                await writeAndFlush(res, createData, dirtySize);

                let tunObj = ctx.get(tunId);
                if (!tunObj) throw new Error("not found");

                await new Promise(resolve => {
                    tunObj.socket.on('data', async (chunk) => {
                        await writeAndFlush(res, marshalBase64(newData(tunId, chunk)), dirtySize);
                    });
                    tunObj.socket.on('close', resolve);
                    tunObj.socket.on('error', resolve);
                });
                break;
            case 0x01:
                performWrite(dataMap, tunId);
                break;
            case 0x02:
                performDelete(tunId);
                break;
            case 0x10:
                await writeAndFlush(res, marshalBase64(newHeartbeat(tunId)), dirtySize);
                break;
        }
    } catch (e) {
        performDelete(tunId);
        await writeAndFlush(res, marshalBase64(newDel(tunId)), dirtySize);
    }
}

async function processClassic(req, dataMap, tunId) {
    let action = dataMap.get("ac")[0];
    try {
        switch (action) {
            case 0x00:
                return await performCreate(dataMap, tunId, false);
            case 0x01:
                performWrite(dataMap, tunId);
                return performRead(tunId);
            case 0x02:
                performDelete(tunId);
                break;
        }
    } catch (e) {
        performDelete(tunId);
        return marshalBase64(newDel(tunId));
    }
    return Buffer.alloc(0);
}

async function performCreate(dataMap, tunId, isHalfStream = false) {
    let host = dataMap.get("h").toString();
    let port = parseInt(dataMap.get("p").toString(), 10);

    let socket = new net.Socket();
    let tunObj = { socket: socket, readQueue: [], isOpen: false };

    return new Promise((resolve) => {
        if (!isHalfStream) {
            socket.on('data', chunk => tunObj.readQueue.push(chunk));
        }
        socket.on('close', () => tunObj.isOpen = false);
        socket.on('error', () => tunObj.isOpen = false);

        socket.setTimeout(3000);
        socket.connect(port, host, () => {
            socket.setTimeout(0); // 取消连接超时
            tunObj.isOpen = true;
            ctx.set(tunId, tunObj);
            resolve(marshalBase64(newStatus(tunId, 0x00)));
        });

        socket.once('error', () => resolve(marshalBase64(newStatus(tunId, 0x01))));
        socket.once('timeout', () => { socket.destroy(); resolve(marshalBase64(newStatus(tunId, 0x01))); });
    });
}

function performWrite(dataMap, tunId) {
    let tunObj = ctx.get(tunId);
    if (!tunObj || !tunObj.isOpen) return;
    let data = dataMap.get("dt");
    if (data && data.length > 0) {
        tunObj.socket.write(data);
    }
}

function performRead(tunId) {
    let tunObj = ctx.get(tunId);
    if (!tunObj) throw new Error("tunnel not found");

    let maxSize = 512 * 1024;
    let written = 0;
    let bufs = [];

    while (tunObj.readQueue.length > 0) {
        let chunk = tunObj.readQueue.shift();
        written += chunk.length;
        bufs.push(marshalBase64(newData(tunId, chunk)));
        if (written >= maxSize) break;
    }

    if (!tunObj.isOpen && tunObj.readQueue.length === 0) {
        performDelete(tunId);
        bufs.push(marshalBase64(newDel(tunId)));
    }

    return Buffer.concat(bufs);
}

function performDelete(tunId) {
    let tunObj = ctx.get(tunId);
    if (tunObj) {
        tunObj.socket.destroy();
        ctx.delete(tunId);
    }
}

async function unmarshalBase64(reader) {
    let m = new Map();
    try {
        let headerBase64 = await reader.read(8);
        if (headerBase64.length === 0) return m;
        let header = b64urlDecode(headerBase64.toString());

        let xor = Buffer.from([header[0], header[1]]);
        for (let i = 2; i < 6; i++) {
            header[i] ^= xor[i % 2];
        }

        let len = header.readUInt32BE(2);
        if (len > 1024 * 1024 * 32 || len < 0) throw new Error("invalid len");

        let bsBase64 = await reader.read(len);
        let bs = b64urlDecode(bsBase64.toString());

        for (let i = 0; i < bs.length; i++) {
            bs[i] ^= xor[i % 2];
        }

        let offset = 0;
        while (offset < bs.length) {
            let kLen = bs[offset++];
            let key = bs.subarray(offset, offset + kLen).toString('utf8');
            offset += kLen;

            let vLen = bs.readUInt32BE(offset);
            offset += 4;

            let value = bs.subarray(offset, offset + vLen);
            offset += vLen;

            m.set(key, value);
        }
    } catch (e) { /* ignore to return partial map */ }
    return m;
}

function marshalBase64(m) {
    let buffers = [];
    let junkSize = Math.floor(Math.random() * 32);
    if (junkSize > 0) {
        let junk = Buffer.alloc(junkSize);
        for(let i=0;i<junkSize;i++) junk[i] = Math.floor(Math.random() * 256);
        m.set('_', junk);
    }

    for (let [key, value] of m.entries()) {
        let kBuf = Buffer.from(key, 'utf8');
        let head = Buffer.alloc(1 + kBuf.length + 4);
        head[0] = kBuf.length;
        kBuf.copy(head, 1);
        head.writeUInt32BE(value.length, 1 + kBuf.length);
        buffers.push(head, value);
    }

    let buf = Buffer.concat(buffers);
    let keyXor = Buffer.from([Math.floor(Math.random()*255)+1, Math.floor(Math.random()*255)+1]);

    for (let i = 0; i < buf.length; i++) {
        buf[i] ^= keyXor[i % 2];
    }
    let dataBuf = Buffer.from(b64urlEncode(buf), 'utf8');

    let dbuf = Buffer.alloc(6);
    keyXor.copy(dbuf, 0);
    dbuf.writeInt32BE(dataBuf.length, 2);
    for(let i=2; i<6; i++) {
        dbuf[i] ^= keyXor[i % 2];
    }
    let headBuf = Buffer.from(b64urlEncode(dbuf), 'utf8');

    return Buffer.concat([headBuf, dataBuf]);
}

class StreamReader {
    constructor(stream) {
        this.stream = stream;
        this.buffer = Buffer.alloc(0);
        this.resolvers = [];
        this.ended = false;

        this.stream.on('data', chunk => {
            this.buffer = Buffer.concat([this.buffer, chunk]);
            this.check();
        });
        this.stream.on('end', () => {
            this.ended = true;
            this.check();
        });
    }

    check() {
        if (this.resolvers.length > 0) {
            const req = this.resolvers[0];
            if (this.buffer.length >= req.len) {
                const result = this.buffer.subarray(0, req.len);
                this.buffer = this.buffer.subarray(req.len);
                this.resolvers.shift();
                req.resolve(result);
                this.check(); // loop again
            } else if (this.ended) {
                this.resolvers.shift();
                req.reject(new Error('EOF'));
            }
        }
    }

    async read(len) {
        if (this.buffer.length >= len) {
            const result = this.buffer.subarray(0, len);
            this.buffer = this.buffer.subarray(len);
            return result;
        }
        if (this.ended) throw new Error('EOF');
        return new Promise((resolve, reject) => {
            this.resolvers.push({ len, resolve, reject });
        });
    }
}

async function writeAndFlush(res, data, dirtySize) {
    if (!data || data.length === 0) return;
    res.write(data);
    if (dirtySize > 0) {
        res.write(marshalBase64(newDirtyChunk(dirtySize)));
    }
}

async function handleRedirect(req, res, dataMap, reader) {
    let redirectData = dataMap.get("r");

    if (redirectData && redirectData.length > 0 && !isLocalAddr(redirectData.toString())) {
        let targetUrlStr = redirectData.toString();

        dataMap.delete("r");

        let headerData = marshalBase64(dataMap);

        let bodyContent = await new Promise((resolve) => {
            if (reader.ended) {
                resolve(reader.buffer);
            } else {
                reader.stream.on('end', () => resolve(reader.buffer));
            }
        });

        let newBody = Buffer.concat([headerData, bodyContent]);

        let targetUrl;
        try {
            targetUrl = new URL(targetUrlStr);
        } catch (e) {
            return false;
        }

        const clientModule = targetUrl.protocol === 'https:' ? https : http;

        const options = {
            method: req.method,
            headers: Object.assign({}, req.headers),
            timeout: 3000,
            rejectUnauthorized: false
        };

        options.headers['content-length'] = newBody.length;
        options.headers['host'] = targetUrl.host;
        options.headers['connection'] = 'close';
        delete options.headers['content-encoding'];
        delete options.headers['transfer-encoding'];

        return new Promise((resolve) => {
            let proxyReq = clientModule.request(targetUrl, options, (proxyRes) => {
                res.writeHead(proxyRes.statusCode, proxyRes.headers);
                proxyRes.pipe(res);
                proxyRes.on('end', () => resolve(true));
            });

            proxyReq.on('error', (err) => {
                if (!res.headersSent) res.writeHead(500);
                res.end();
                resolve(true);
            });

            proxyReq.write(newBody);
            proxyReq.end();
        });
    }
    return false;
}

function processTemplateStart(res, sid) {
    let tpl = ctx.get(sid);
    if (!tpl) return Buffer.alloc(0);
    if (tpl.ct) res.setHeader("Content-Type", tpl.ct);
    return Buffer.from(tpl.start || "");
}

function processTemplateEnd(sid) {
    let tpl = ctx.get(sid);
    if (!tpl) return Buffer.alloc(0);
    return Buffer.from(tpl.end || "");
}

function setBypassHeader(res) {
    res.setHeader("X-Accel-Buffering", "no");
}

function newStatus(tunId, b) {
    let m = new Map();
    m.set("ac", Buffer.from([0x03]));
    m.set("s", Buffer.from([b]));
    m.set("id", Buffer.from(tunId));
    return m;
}

function newData(tunId, data) {
    let m = new Map();
    m.set("ac", Buffer.from([0x01]));
    m.set("dt", Buffer.from(data));
    m.set("id", Buffer.from(tunId));
    return m;
}

function newDel(tunId) {
    let m = new Map();
    m.set("ac", Buffer.from([0x02]));
    m.set("id", Buffer.from(tunId));
    return m;
}

function newHeartbeat(tunId) {
    let m = new Map();
    m.set("ac", Buffer.from([0x10]));
    m.set("id", Buffer.from(tunId));
    return m;
}

function newDirtyChunk(size) {
    let m = new Map();
    m.set("ac", Buffer.from([0x11]));
    if (size > 0) {
        let junk = Buffer.alloc(size);
        for(let i=0; i<size; i++) junk[i] = Math.floor(Math.random() * 256);
        m.set("d", junk);
    }
    return m;
}

function b64urlEncode(buf) {
    return buf.toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '');
}

function b64urlDecode(str) {
    let pad = str.length % 4;
    if (pad) str += '='.repeat(4 - pad);
    return Buffer.from(str.replace(/-/g, '+').replace(/_/g, '/'), 'base64');
}

function randomString(length) {
    let res = "";
    for (let i = 0; i < length; i++) {
        res += CHARACTERS.charAt(Math.floor(Math.random() * CHARACTERS.length));
    }
    return res;
}

function collectAddr() {
    let addrsMap = new Map();
    let nifs = os.networkInterfaces();
    for (let key in nifs) {
        for (let net of nifs[key]) {
            if (net.family === 'IPv4' || net.family === 'IPv6') {
                let ip = net.address.split('%')[0];
                addrsMap.set(ip, true);
            }
        }
    }
    return addrsMap;
}

function isLocalAddr(urlStr) {
    try {
        let u = new URL(urlStr);
        return addrs.has(u.hostname);
    } catch (e) { return false; }
}

server.listen(8080, () => {
    console.log("Node.js Suo5 Tunneling Server running on port 8080");
});