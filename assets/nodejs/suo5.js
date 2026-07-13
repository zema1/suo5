'use strict';

const http = require('http');
const https = require('https');
const net = require('net');
const os = require('os');

const ctx = new Map();
const addrs = collectAddr();
const CHARACTERS = 'abcdefghijklmnopqrstuvwxyz0123456789';
const MAX_FRAME_SIZE = 32 * 1024 * 1024;
const MAX_READ_SIZE = 512 * 1024;
const MAX_QUEUE_SIZE = 8 * 1024 * 1024;
const TUNNEL_IDLE_TIMEOUT_MS = 300 * 1000;
const SESSION_IDLE_TIMEOUT_MS = 300 * 1000;

const cleanupTimer = setInterval(cleanupIdleState, 30 * 1000);
if (cleanupTimer.unref) cleanupTimer.unref();

// SUO5_SERVER_START
const server = http.createServer((req, res) => {
    processRequest(req, res).catch(() => {
        if (!res.headersSent) res.writeHead(500);
        if (!res.writableEnded) res.end();
    });
});

server.on('close', closeAll);
// SUO5_SERVER_END

async function processRequest(req, res) {
    const reader = new StreamReader(req);
    let dataMap;
    try {
        dataMap = await unmarshalBase64(reader);
    } catch (_) {
        if (!res.writableEnded) res.end();
        return;
    }

    if (!isValidFrame(dataMap, true)) {
        res.end();
        return;
    }

    let sidData = dataMap.get('sid');
    let sid = sidData ? sidData.toString() : null;
    let tunId = dataMap.get('id').toString();
    const mode = dataMap.get('m')[0];

    switch (mode) {
        case 0x00:
            sid = randomString(16);
            await processHandshake(req, res, dataMap, tunId, sid);
            return;
        case 0x01:
            setBypassHeader(res);
            await processFullStream(req, reader, res, dataMap, tunId);
            return;
        case 0x02:
            setBypassHeader(res);
            // fall through
        case 0x03:
            if (await handleRedirect(req, res, dataMap, reader)) return;
            const session = sid ? getSession(sid) : null;
            if (!session) {
                res.writeHead(403);
                res.end();
                return;
            }
            touch(session);
            if (mode === 0x02) {
                await processHalfRequest(req, reader, res, dataMap, tunId, session);
            } else {
                await processClassicRequest(req, reader, res, dataMap, tunId, session);
            }
            return;
        default:
            res.end();
    }
}

async function processHandshake(req, res, dataMap, tunId, sid) {
    const redirectData = dataMap.get('r');
    if (redirectData && redirectData.length > 0 && !isLocalAddr(redirectData.toString())) {
        res.writeHead(403);
        res.end();
        return;
    }

    const tplData = dataMap.get('tpl');
    const ctData = dataMap.get('ct');
    const parts = tplData && ctData ? tplData.toString().split('#data#') : ['', ''];
    const jkData = dataMap.get('jk');
    const dirtySize = jkData ? Math.max(0, parseInt(jkData.toString(), 10) || 0) : 0;
    const session = {
        kind: 'session',
        ct: ctData ? ctData.toString() : '',
        start: parts[0] || '',
        end: parts[1] || '',
        dirtySize,
        lastActivity: Date.now()
    };
    ctx.set(sid, session);

    const isAutoData = dataMap.get('a');
    const isAuto = isAutoData && isAutoData[0] === 0x01;
    if (isAuto) {
        setBypassHeader(res);
        await writeAndFlush(res, processTemplateStart(res, session), 0);
        await writeAndFlush(res, marshalBase64(newData(tunId, dataMap.get('dt'))), 0);
        await delay(2000);
        await writeAndFlush(res, marshalBase64(newData(tunId, Buffer.from(sid))), 0);
        await writeAndFlush(res, processTemplateEnd(session), 0);
        res.end();
        return;
    }

    const body = Buffer.concat([
        processTemplateStart(res, session),
        marshalBase64(newData(tunId, dataMap.get('dt'))),
        marshalBase64(newData(tunId, Buffer.from(sid))),
        processTemplateEnd(session)
    ]);
    res.setHeader('Content-Length', body.length);
    await writeAndFlush(res, body, 0);
    res.end();
}

async function processFullStream(req, reader, res, dataMap, tunId) {
    const target = getTarget(req, dataMap);
    const socket = new net.Socket();
    let responseWrites = Promise.resolve();
    let outputDone = false;
    let idleTimer = null;

    const resetIdleTimer = () => {
        clearTimeout(idleTimer);
        idleTimer = setTimeout(() => socket.destroy(new Error('tunnel idle timeout')), TUNNEL_IDLE_TIMEOUT_MS);
        if (idleTimer.unref) idleTimer.unref();
    };
    const abort = () => socket.destroy();
    const onResponseClose = () => {
        if (!res.writableEnded) abort();
    };
    req.once('aborted', abort);
    req.once('error', abort);
    res.once('close', onResponseClose);
    socket.on('error', () => {});

    try {
        await connectSocket(socket, target.host, target.port, 5000);
        resetIdleTimer();
        await writeAndFlush(res, marshalBase64(newStatus(tunId, 0x00)), 0);
    } catch (_) {
        await safeWrite(res, marshalBase64(newStatus(tunId, 0x01)), 0);
        if (!res.writableEnded) res.end();
        socket.destroy();
        clearTimeout(idleTimer);
        removeAbortListeners();
        return;
    }

    const inputTask = (async () => {
        try {
            while (true) {
                const frame = await unmarshalBase64(reader);
                if (!isValidFrame(frame, false)) break;
                const action = frame.get('ac')[0];
                resetIdleTimer();
                if (action === 0x01) {
                    const data = frame.get('dt');
                    if (data && data.length) await writeSocket(socket, data);
                } else if (action === 0x02) {
                    break;
                } else if (action === 0x10) {
                    responseWrites = responseWrites.then(() => writeAndFlush(res, marshalBase64(newHeartbeat(tunId)), 0));
                    await responseWrites;
                }
            }
        } catch (_) {
            // EOF and client abort both terminate the target side.
        } finally {
            if (!outputDone) socket.destroy();
        }
    })();

    socket.on('data', chunk => {
        socket.pause();
        resetIdleTimer();
        responseWrites = responseWrites
            .then(() => writeAndFlush(res, marshalBase64(newData(tunId, chunk)), 0))
            .then(() => {
                if (!socket.destroyed) socket.resume();
            })
            .catch(abort);
    });

    await onceSocketClosed(socket);
    outputDone = true;
    reader.cancel();
    await inputTask.catch(() => {});
    await responseWrites.catch(() => {});
    await safeWrite(res, marshalBase64(newDel(tunId)), 0);
    if (!res.writableEnded && !res.destroyed) res.end();
    clearTimeout(idleTimer);
    removeAbortListeners();

    function removeAbortListeners() {
        req.removeListener('aborted', abort);
        req.removeListener('error', abort);
        res.removeListener('close', onResponseClose);
    }
}

async function processHalfRequest(req, reader, res, firstFrame, firstTunId, session) {
    await writeAndFlush(res, processTemplateStart(res, session), session.dirtySize);
    let frame = firstFrame;
    let tunId = firstTunId;
    while (true) {
        await processHalfFrame(req, res, frame, tunId, session.dirtySize);
        try {
            frame = await unmarshalBase64(reader);
            if (!isValidFrame(frame, false)) break;
            tunId = frame.get('id').toString();
        } catch (_) {
            break;
        }
    }
    await safeWrite(res, processTemplateEnd(session), session.dirtySize);
    if (!res.writableEnded && !res.destroyed) res.end();
}

async function processHalfFrame(req, res, dataMap, tunId, dirtySize) {
    const action = dataMap.get('ac')[0];
    try {
        if (action === 0x00) {
            // A retry needs a fresh response owner. Never leave the old target socket orphaned.
            const old = getTunnel(tunId);
            if (old) performDelete(tunId, old);
            const createData = await performCreate(req, dataMap, tunId, true);
            await writeAndFlush(res, createData, dirtySize);
            const tunnel = getTunnel(tunId);
            if (!tunnel || !tunnel.isOpen) throw new Error('tunnel not found');
            await streamHalfTunnel(res, tunId, tunnel, dirtySize);
        } else if (action === 0x01) {
            await performWrite(dataMap, tunId);
        } else if (action === 0x02) {
            performDelete(tunId);
        } else if (action === 0x10) {
            const tunnel = getTunnel(tunId);
            if (tunnel) touch(tunnel);
            await writeAndFlush(res, marshalBase64(newHeartbeat(tunId)), dirtySize);
        }
    } catch (_) {
        performDelete(tunId);
        await safeWrite(res, marshalBase64(newDel(tunId)), dirtySize);
    }
}

async function streamHalfTunnel(res, tunId, tunnel, dirtySize) {
    let writeChain = Promise.resolve();
    let finished = false;
    const abort = () => {
        if (!finished) performDelete(tunId, tunnel);
    };
    res.once('close', abort);
    tunnel.socket.on('data', chunk => {
        tunnel.socket.pause();
        touch(tunnel);
        writeChain = writeChain
            .then(() => writeAndFlush(res, marshalBase64(newData(tunId, chunk)), dirtySize))
            .then(() => {
                if (!tunnel.socket.destroyed) tunnel.socket.resume();
            })
            .catch(abort);
    });
    await onceSocketClosed(tunnel.socket);
    await writeChain.catch(() => {});
    finished = true;
    res.removeListener('close', abort);
    performDelete(tunId, tunnel);
    await safeWrite(res, marshalBase64(newDel(tunId)), dirtySize);
}

async function processClassicRequest(req, reader, res, firstFrame, firstTunId, session) {
    const buffers = [processTemplateStart(res, session)];
    let frame = firstFrame;
    let tunId = firstTunId;
    while (true) {
        const data = await processClassicFrame(req, frame, tunId);
        if (data && data.length) buffers.push(data);
        try {
            frame = await unmarshalBase64(reader);
            if (!isValidFrame(frame, false)) break;
            tunId = frame.get('id').toString();
        } catch (_) {
            break;
        }
    }
    buffers.push(processTemplateEnd(session));
    const body = Buffer.concat(buffers);
    res.setHeader('Content-Length', body.length);
    await writeAndFlush(res, body, 0);
    res.end();
}

async function processClassicFrame(req, dataMap, tunId) {
    const action = dataMap.get('ac')[0];
    try {
        if (action === 0x00) return await performCreate(req, dataMap, tunId, false);
        if (action === 0x01) {
            await performWrite(dataMap, tunId);
            return performRead(tunId);
        }
        if (action === 0x02) performDelete(tunId);
    } catch (_) {
        performDelete(tunId);
        return marshalBase64(newDel(tunId));
    }
    return Buffer.alloc(0);
}

async function performCreate(req, dataMap, tunId, isHalfStream) {
    let existing = getTunnel(tunId);
    if (existing) {
        touch(existing);
        await existing.ready.catch(() => {});
        return marshalBase64(newStatus(tunId, existing.isOpen ? 0x00 : 0x01));
    }

    const target = getTarget(req, dataMap);
    const socket = new net.Socket();
    const tunnel = {
        kind: 'tunnel', socket, isOpen: false, readQueue: [], readBytes: 0,
        writeChain: Promise.resolve(), lastActivity: Date.now(), ready: null
    };
    ctx.set(tunId, tunnel);
    socket.on('error', () => {
        tunnel.isOpen = false;
    });
    socket.on('close', () => {
        tunnel.isOpen = false;
        notifyTunnel(tunnel);
    });
    if (!isHalfStream) {
        socket.on('data', chunk => enqueueClassicData(tunnel, chunk));
    }

    tunnel.ready = connectSocket(socket, target.host, target.port, 3000)
        .then(() => {
            tunnel.isOpen = true;
            touch(tunnel);
        })
        .catch(err => {
            removeIfSame(tunId, tunnel);
            socket.destroy();
            throw err;
        });
    try {
        await tunnel.ready;
        return marshalBase64(newStatus(tunId, 0x00));
    } catch (_) {
        return marshalBase64(newStatus(tunId, 0x01));
    }
}

async function performWrite(dataMap, tunId) {
    const tunnel = getTunnel(tunId);
    if (!tunnel || !tunnel.isOpen) return;
    const data = dataMap.get('dt');
    if (!data || !data.length) return;
    touch(tunnel);
    const operation = tunnel.writeChain.then(() => writeSocket(tunnel.socket, data));
    tunnel.writeChain = operation.catch(() => {});
    await operation;
}

function performRead(tunId) {
    const tunnel = getTunnel(tunId);
    if (!tunnel) throw new Error('tunnel not found');
    touch(tunnel);
    let written = 0;
    const buffers = [];
    while (tunnel.readQueue.length) {
        const chunk = tunnel.readQueue.shift();
        tunnel.readBytes -= chunk.length;
        written += chunk.length;
        buffers.push(marshalBase64(newData(tunId, chunk)));
        if (written >= MAX_READ_SIZE) break;
    }
    if (tunnel.socket.isPaused() && tunnel.readBytes < MAX_QUEUE_SIZE / 2 && tunnel.isOpen) {
        tunnel.socket.resume();
    }
    if (!tunnel.isOpen && tunnel.readQueue.length === 0) {
        performDelete(tunId, tunnel);
        buffers.push(marshalBase64(newDel(tunId)));
    }
    return Buffer.concat(buffers);
}

function enqueueClassicData(tunnel, chunk) {
    if (!chunk.length) return;
    tunnel.readQueue.push(chunk);
    tunnel.readBytes += chunk.length;
    touch(tunnel);
    if (tunnel.readBytes >= MAX_QUEUE_SIZE) tunnel.socket.pause();
    notifyTunnel(tunnel);
}

function performDelete(tunId, expected) {
    const tunnel = getTunnel(tunId);
    if (!tunnel || (expected && tunnel !== expected)) return;
    removeIfSame(tunId, tunnel);
    tunnel.isOpen = false;
    tunnel.socket.destroy();
    notifyTunnel(tunnel);
}

function getTunnel(tunId) {
    const value = ctx.get(tunId);
    return value && value.kind === 'tunnel' ? value : null;
}

function getSession(sid) {
    const value = ctx.get(sid);
    return value && value.kind === 'session' ? value : null;
}

function removeIfSame(key, expected) {
    if (ctx.get(key) === expected) ctx.delete(key);
}

function touch(value) {
    value.lastActivity = Date.now();
}

function cleanupIdleState() {
    const now = Date.now();
    for (const [key, value] of ctx) {
        const timeout = value.kind === 'tunnel' ? TUNNEL_IDLE_TIMEOUT_MS : SESSION_IDLE_TIMEOUT_MS;
        if (now - value.lastActivity < timeout) continue;
        if (value.kind === 'tunnel') performDelete(key, value);
        else removeIfSame(key, value);
    }
}

function closeAll() {
    clearInterval(cleanupTimer);
    for (const [key, value] of ctx) {
        if (value.kind === 'tunnel') performDelete(key, value);
        else ctx.delete(key);
    }
}

function notifyTunnel(tunnel) {
    if (!tunnel.waiters) return;
    for (const resolve of tunnel.waiters) resolve();
    tunnel.waiters.clear();
}

async function unmarshalBase64(reader) {
    const headerBase64 = await reader.read(8);
    const header = b64urlDecode(headerBase64.toString());
    if (header.length !== 6) throw new Error('invalid header');
    const xor = Buffer.from([header[0], header[1]]);
    for (let i = 2; i < 6; i++) header[i] ^= xor[i % 2];
    const len = header.readUInt32BE(2);
    if (len > MAX_FRAME_SIZE) throw new Error('invalid len');
    const encoded = await reader.read(len);
    const body = b64urlDecode(encoded.toString());
    for (let i = 0; i < body.length; i++) body[i] ^= xor[i % 2];

    const result = new Map();
    let offset = 0;
    while (offset < body.length) {
        if (offset + 1 > body.length) throw new Error('invalid key len');
        const keyLen = body[offset++];
        if (offset + keyLen + 4 > body.length) throw new Error('invalid key');
        const key = body.subarray(offset, offset + keyLen).toString('utf8');
        offset += keyLen;
        const valueLen = body.readUInt32BE(offset);
        offset += 4;
        if (offset + valueLen > body.length) throw new Error('invalid value');
        result.set(key, body.subarray(offset, offset + valueLen));
        offset += valueLen;
    }
    return result;
}

function marshalBase64(dataMap) {
    const entries = new Map(dataMap);
    const junkSize = Math.floor(Math.random() * 32);
    if (junkSize) {
        const junk = Buffer.allocUnsafe(junkSize);
        for (let i = 0; i < junkSize; i++) junk[i] = Math.floor(Math.random() * 256);
        entries.set('_', junk);
    }
    const buffers = [];
    for (const [key, value] of entries) {
        const keyBuffer = Buffer.from(key, 'utf8');
        if (keyBuffer.length > 255) throw new Error('key too long');
        const head = Buffer.alloc(1 + keyBuffer.length + 4);
        head[0] = keyBuffer.length;
        keyBuffer.copy(head, 1);
        head.writeUInt32BE(value.length, 1 + keyBuffer.length);
        buffers.push(head, value);
    }
    const body = Buffer.concat(buffers);
    const xor = Buffer.from([randomByte(), randomByte()]);
    for (let i = 0; i < body.length; i++) body[i] ^= xor[i % 2];
    const encoded = Buffer.from(b64urlEncode(body), 'utf8');
    const header = Buffer.alloc(6);
    xor.copy(header);
    header.writeUInt32BE(encoded.length, 2);
    for (let i = 2; i < 6; i++) header[i] ^= xor[i % 2];
    return Buffer.concat([Buffer.from(b64urlEncode(header), 'utf8'), encoded]);
}

class StreamReader {
    constructor(stream) {
        this.stream = stream;
        this.buffer = Buffer.alloc(0);
        this.waiters = [];
        this.ended = false;
        this.error = null;
        this.onData = chunk => {
            if (this.buffer.length + chunk.length > MAX_FRAME_SIZE + 8) {
                this.finish(new Error('request buffer limit exceeded'));
                stream.destroy();
                return;
            }
            this.buffer = Buffer.concat([this.buffer, chunk]);
            this.check();
        };
        this.onEnd = () => this.finish(new Error('EOF'));
        this.onAborted = () => this.finish(new Error('request aborted'));
        this.onError = err => this.finish(err);
        stream.on('data', this.onData);
        stream.once('end', this.onEnd);
        stream.once('aborted', this.onAborted);
        stream.once('error', this.onError);
    }

    read(len) {
        if (!Number.isSafeInteger(len) || len < 0 || len > MAX_FRAME_SIZE) {
            return Promise.reject(new Error('invalid read length'));
        }
        if (this.buffer.length >= len) return Promise.resolve(this.take(len));
        if (this.ended) return Promise.reject(this.error || new Error('EOF'));
        return new Promise((resolve, reject) => {
            this.waiters.push({ len, resolve, reject });
            this.check();
        });
    }

    take(len) {
        const result = this.buffer.subarray(0, len);
        this.buffer = this.buffer.subarray(len);
        return result;
    }

    check() {
        while (this.waiters.length && this.buffer.length >= this.waiters[0].len) {
            const waiter = this.waiters.shift();
            waiter.resolve(this.take(waiter.len));
        }
        if (this.ended) {
            while (this.waiters.length) this.waiters.shift().reject(this.error || new Error('EOF'));
        }
    }

    finish(err) {
        if (this.ended) return;
        this.ended = true;
        this.error = err;
        this.check();
    }

    async readRemaining() {
        if (!this.ended) {
            await new Promise((resolve, reject) => {
                const done = () => {
                    this.stream.removeListener('error', failed);
                    resolve();
                };
                const failed = err => {
                    this.stream.removeListener('end', done);
                    reject(err);
                };
                this.stream.once('end', done);
                this.stream.once('error', failed);
            });
        }
        const result = this.buffer;
        this.buffer = Buffer.alloc(0);
        return result;
    }

    cancel() {
        this.finish(new Error('reader cancelled'));
        this.stream.removeListener('data', this.onData);
        this.stream.removeListener('end', this.onEnd);
        this.stream.removeListener('aborted', this.onAborted);
        this.stream.removeListener('error', this.onError);
    }
}

async function writeAndFlush(res, data, dirtySize) {
    if (!data || !data.length) return;
    await writeResponse(res, data);
    if (dirtySize > 0) await writeResponse(res, marshalBase64(newDirtyChunk(dirtySize)));
}

async function safeWrite(res, data, dirtySize) {
    try {
        await writeAndFlush(res, data, dirtySize);
    } catch (_) {}
}

function writeResponse(res, data) {
    if (res.destroyed || res.writableEnded) return Promise.reject(new Error('response closed'));
    if (res.write(data)) return Promise.resolve();
    return new Promise((resolve, reject) => {
        const cleanup = () => {
            res.removeListener('drain', onDrain);
            res.removeListener('error', onError);
            res.removeListener('close', onClose);
        };
        const onDrain = () => { cleanup(); resolve(); };
        const onError = err => { cleanup(); reject(err); };
        const onClose = () => { cleanup(); reject(new Error('response closed')); };
        res.once('drain', onDrain);
        res.once('error', onError);
        res.once('close', onClose);
    });
}

function writeSocket(socket, data) {
    if (socket.destroyed || !socket.writable) return Promise.reject(new Error('socket closed'));
    if (socket.write(data)) return Promise.resolve();
    return new Promise((resolve, reject) => {
        const cleanup = () => {
            socket.removeListener('drain', onDrain);
            socket.removeListener('error', onError);
            socket.removeListener('close', onClose);
        };
        const onDrain = () => { cleanup(); resolve(); };
        const onError = err => { cleanup(); reject(err); };
        const onClose = () => { cleanup(); reject(new Error('socket closed')); };
        socket.once('drain', onDrain);
        socket.once('error', onError);
        socket.once('close', onClose);
    });
}

async function handleRedirect(req, res, dataMap, reader) {
    const redirectData = dataMap.get('r');
    if (!redirectData || !redirectData.length || isLocalAddr(redirectData.toString())) return false;
    let target;
    try {
        target = new URL(redirectData.toString());
        if (target.protocol !== 'http:' && target.protocol !== 'https:') throw new Error('invalid protocol');
    } catch (_) {
        return false;
    }

    dataMap.delete('r');
    const body = Buffer.concat([marshalBase64(dataMap), await reader.readRemaining()]);
    const headers = Object.assign({}, req.headers, {
        host: target.host,
        connection: 'close',
        'content-length': body.length
    });
    delete headers['content-encoding'];
    delete headers['transfer-encoding'];
    const client = target.protocol === 'https:' ? https : http;

    await new Promise(resolve => {
        const proxyReq = client.request(target, {
            method: req.method,
            headers,
            rejectUnauthorized: false
        }, proxyRes => {
            res.writeHead(proxyRes.statusCode || 502, proxyRes.headers);
            proxyRes.pipe(res);
            proxyRes.once('end', resolve);
            proxyRes.once('error', () => {
                if (!res.writableEnded) res.end();
                resolve();
            });
        });
        const timer = setTimeout(() => proxyReq.destroy(new Error('redirect timeout')), 3000);
        const done = () => clearTimeout(timer);
        proxyReq.once('close', done);
        proxyReq.once('error', () => {
            if (!res.headersSent) res.writeHead(502);
            if (!res.writableEnded) res.end();
            resolve();
        });
        res.once('close', () => proxyReq.destroy());
        proxyReq.end(body);
    });
    return true;
}

function connectSocket(socket, host, port, timeout) {
    return new Promise((resolve, reject) => {
        let settled = false;
        const finish = err => {
            if (settled) return;
            settled = true;
            socket.setTimeout(0);
            socket.removeListener('connect', onConnect);
            socket.removeListener('error', onError);
            socket.removeListener('timeout', onTimeout);
            if (err) reject(err); else resolve();
        };
        const onConnect = () => finish();
        const onError = err => finish(err);
        const onTimeout = () => finish(new Error('connect timeout'));
        socket.setNoDelay(true);
        socket.setTimeout(timeout);
        socket.once('connect', onConnect);
        socket.once('error', onError);
        socket.once('timeout', onTimeout);
        socket.connect(port, host);
    });
}

function onceSocketClosed(socket) {
    if (socket.destroyed) return Promise.resolve();
    return new Promise(resolve => socket.once('close', resolve));
}

function getTarget(req, dataMap) {
    const hostData = dataMap.get('h');
    const portData = dataMap.get('p');
    if (!hostData || !portData) throw new Error('missing target');
    const host = hostData.toString();
    let port = parseInt(portData.toString(), 10);
    if (port === 0) port = req.socket.localPort;
    if (!host || !Number.isInteger(port) || port < 1 || port > 65535) throw new Error('invalid target');
    return { host, port };
}

function isValidFrame(dataMap, requireMode) {
    if (!dataMap || !(dataMap instanceof Map)) return false;
    const action = dataMap.get('ac');
    const id = dataMap.get('id');
    const mode = dataMap.get('m');
    return !!(action && action.length === 1 && id && id.length && (!requireMode || (mode && mode.length)));
}

function processTemplateStart(res, session) {
    if (session.ct) res.setHeader('Content-Type', session.ct);
    return Buffer.from(session.start);
}

function processTemplateEnd(session) {
    return Buffer.from(session.end);
}

function setBypassHeader(res) {
    res.setHeader('X-Accel-Buffering', 'no');
}

function newStatus(tunId, status) {
    return new Map([['ac', Buffer.from([0x03])], ['s', Buffer.from([status])], ['id', Buffer.from(tunId)]]);
}

function newData(tunId, data) {
    return new Map([['ac', Buffer.from([0x01])], ['dt', Buffer.from(data || Buffer.alloc(0))], ['id', Buffer.from(tunId)]]);
}

function newDel(tunId) {
    return new Map([['ac', Buffer.from([0x02])], ['id', Buffer.from(tunId)]]);
}

function newHeartbeat(tunId) {
    return new Map([['ac', Buffer.from([0x10])], ['id', Buffer.from(tunId)]]);
}

function newDirtyChunk(size) {
    const result = new Map([['ac', Buffer.from([0x11])]]);
    if (size > 0) {
        const junk = Buffer.allocUnsafe(size);
        for (let i = 0; i < size; i++) junk[i] = Math.floor(Math.random() * 256);
        result.set('d', junk);
    }
    return result;
}

function b64urlEncode(buf) {
    return buf.toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '');
}

function b64urlDecode(value) {
    const pad = value.length % 4;
    if (pad) value += '='.repeat(4 - pad);
    return Buffer.from(value.replace(/-/g, '+').replace(/_/g, '/'), 'base64');
}

function randomByte() {
    return Math.floor(Math.random() * 255) + 1;
}

function randomString(length) {
    let value = '';
    for (let i = 0; i < length; i++) value += CHARACTERS.charAt(Math.floor(Math.random() * CHARACTERS.length));
    return value;
}

function collectAddr() {
    const result = new Map();
    try {
        const interfaces = os.networkInterfaces();
        for (const key in interfaces) {
            for (const iface of interfaces[key]) {
                if (iface.family === 'IPv4' || iface.family === 4 || iface.family === 'IPv6' || iface.family === 6) {
                    result.set(iface.address.split('%')[0], true);
                }
            }
        }
    } catch (_) {}
    return result;
}

function isLocalAddr(urlValue) {
    try {
        return addrs.has(new URL(urlValue).hostname);
    } catch (_) {
        return false;
    }
}

function delay(ms) {
    return new Promise(resolve => setTimeout(resolve, ms));
}

// SUO5_LISTEN_START
server.listen(8080, () => {
    console.log('Node.js Suo5 Tunneling Server running on port 8080');
});
// SUO5_LISTEN_END
