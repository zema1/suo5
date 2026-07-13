'use strict';

const http = require('http');

const targetPort = Number(process.env.TARGET_PORT || 9998);
const controlPort = Number(process.env.CONTROL_PORT || 9999);
const sockets = new Set();

const target = http.createServer((req, res) => {
    const chunks = [];
    req.on('data', chunk => chunks.push(chunk));
    req.on('end', () => {
        const body = Buffer.concat(chunks);
        res.setHeader('Content-Type', 'application/octet-stream');
        res.setHeader('Content-Length', body.length);
        res.end(body);
    });
});

target.on('connection', socket => {
    sockets.add(socket);
    socket.on('close', () => sockets.delete(socket));
});

const control = http.createServer((req, res) => {
    if (req.url !== '/connections') {
        res.writeHead(404);
        res.end();
        return;
    }
    const body = String(sockets.size);
    res.setHeader('Content-Length', Buffer.byteLength(body));
    res.end(body);
});

target.listen(targetPort, '127.0.0.1');
control.listen(controlPort, '127.0.0.1');

function shutdown() {
    for (const socket of sockets) socket.destroy();
    target.close();
    control.close();
}

process.once('SIGTERM', shutdown);
process.once('SIGINT', shutdown);
