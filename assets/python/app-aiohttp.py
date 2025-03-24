import asyncio
import socket
import ssl
import random
import struct
import ipaddress
import urllib.parse
from typing import Dict, Any, List, Tuple, Optional, Set, Union
import aiohttp
from aiohttp import web
import netifaces

# 全局变量
ctx: Dict[str, Any] = {}
addrs: Set[str] = set()

# 收集本地地址
def collect_addrs() -> Set[str]:
    result = set()
    for interface in netifaces.interfaces():
        addresses = netifaces.ifaddresses(interface)
        for addr_family in (netifaces.AF_INET, netifaces.AF_INET6):
            if addr_family in addresses:
                for addr in addresses[addr_family]:
                    ip = addr['addr']
                    # 处理IPv6地址中的接口标识符
                    if '%' in ip:
                        ip = ip.split('%')[0]
                    result.add(ip)
    return result

# 初始化本地地址
addrs = collect_addrs()

# 数据序列化函数
def marshal(data_map: Dict[str, bytes]) -> bytes:
    buffer = bytearray()
    for key, value in data_map.items():
        key_bytes = key.encode()
        buffer.append(len(key_bytes))
        buffer.extend(key_bytes)
        buffer.extend(struct.pack('>I', len(value)))
        buffer.extend(value)
    
    # 添加长度和XOR密钥
    data = bytes(buffer)
    key = random.randint(1, 255)
    xored_data = bytes(b ^ key for b in data)
    
    return struct.pack('>IB', len(data), key) + xored_data

# 数据反序列化函数
async def unmarshal(reader) -> Dict[str, bytes]:
    # 读取头部(大小和密钥)
    header = await reader.readexactly(5)
    data_len = struct.unpack('>I', header[:4])[0]
    xor_key = header[4]
    
    if data_len > 32 * 1024 * 1024:  # 32MB限制
        raise ValueError("Invalid data length")
    
    # 读取并解密数据
    encrypted_data = await reader.readexactly(data_len)
    data = bytes(b ^ xor_key for b in encrypted_data)
    
    # 解析数据
    result = {}
    i = 0
    while i < len(data) - 1:
        key_len = data[i]
        i += 1
        
        if i + key_len > len(data):
            raise ValueError("Key length error")
        
        key = data[i:i+key_len].decode()
        i += key_len
        
        if i + 4 > len(data):
            raise ValueError("Value length error")
        
        value_len = struct.unpack('>I', data[i:i+4])[0]
        i += 4
        
        if i + value_len > len(data):
            raise ValueError("Value error")
        
        value = data[i:i+value_len]
        i += value_len
        
        result[key] = value
    
    return result

# 辅助函数
def new_status(status: int) -> Dict[str, bytes]:
    return {"s": bytes([status])}

def new_data(data: bytes) -> Dict[str, bytes]:
    return {"ac": bytes([0x01]), "dt": data}

def new_del() -> Dict[str, bytes]:
    return {"ac": bytes([0x02])}

# 是否本地地址
def is_local_addr(url: str) -> bool:
    parsed_url = urllib.parse.urlparse(url)
    host = parsed_url.hostname
    return host in addrs

# 主要请求处理器
class Suo5Handler:
    def __init__(self):
        self.ssl_context = ssl.create_default_context()
        self.ssl_context.check_hostname = False
        self.ssl_context.verify_mode = ssl.CERT_NONE
    
    async def handle_request(self, request: web.Request) -> web.Response:
        # 检查User-Agent
        user_agent = request.headers.get('User-Agent')
        content_type = request.headers.get('Content-Type')
        
        if user_agent != "Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.1.2.3":
            return web.Response(status=404)
        
        if not content_type:
            return web.Response(status=404)
        
        try:
            if content_type == "application/plain":
                return await self.try_full_duplex(request)
            elif content_type == "application/octet-stream":
                return await self.process_data_bio(request)
            else:
                return await self.process_data_unary(request)
        except Exception:
            # 静默处理错误以匹配Java版行为
            return web.Response(status=500)
    
    async def try_full_duplex(self, request: web.Request) -> web.Response:
        # 修正: 读取全部数据然后只取前32字节
        data = await request.read()
        data = data[:32]  # 只获取前32字节
        
        response = web.StreamResponse()
        await response.prepare(request)
        await response.write(data)
        await response.write_eof()
        return response
    
    async def process_data_bio(self, request: web.Request) -> web.Response:
        # 创建流式响应
        response = web.StreamResponse()
        response.headers['X-Accel-Buffering'] = 'no'
        
        # 解析请求
        data_map = await unmarshal(request.content)
        
        action = data_map.get("ac")
        if not action or len(action) != 1 or action[0] != 0x00:
            return web.Response(status=403)
        
        await response.prepare(request)
        
        # 创建socket连接
        try:
            host = data_map.get("h").decode()
            port = int(data_map.get("p").decode())
            
            if port == 0:
                port = request.transport.get_extra_info('sockname')[1]
            
            reader_socket, writer_socket = await asyncio.open_connection(host, port)
            await response.write(marshal(new_status(0x00)))
        except Exception:
            await response.write(marshal(new_status(0x01)))
            await response.write_eof()
            return response
        
        # 设置双向数据流
        try:
            pipe1 = asyncio.create_task(self.pipe_socket_to_http(reader_socket, response))
            pipe2 = asyncio.create_task(self.pipe_http_to_socket(request.content, writer_socket))
            
            # 等待任一管道完成
            done, pending = await asyncio.wait(
                [pipe1, pipe2],
                return_when=asyncio.FIRST_COMPLETED
            )
            
            # 取消未完成的任务
            for task in pending:
                task.cancel()
            
            # 等待取消完成
            await asyncio.gather(*pending, return_exceptions=True)
            
        except Exception:
            pass
        finally:
            writer_socket.close()
            await writer_socket.wait_closed()
            
        return response
    
    async def process_data_unary(self, request: web.Request) -> web.Response:
        data_map = await unmarshal(request.content)
        
        client_id = data_map.get("id").decode()
        action = data_map.get("ac")[0]
        redirect_data = data_map.get("r")
        
        need_redirect = False
        redirect_url = ""
        
        if redirect_data:
            redirect_url = redirect_data.decode()
            need_redirect = not is_local_addr(redirect_url)
            if need_redirect:
                data_map.pop("r", None)
        
        # 处理重定向的非创建请求
        if need_redirect and 0x01 <= action <= 0x03:
            await self.redirect(request, data_map, redirect_url)
            return web.Response()
        
        response = web.StreamResponse()
        
        # 处理删除请求
        if action == 0x02:
            socket_writer = ctx.get(client_id)
            if socket_writer:
                socket_writer.close()
                ctx.pop(client_id, None)
            return web.Response()
        
        # 处理数据请求
        elif action == 0x01:
            socket_writer = ctx.get(client_id)
            if not socket_writer:
                await response.prepare(request)
                await response.write(marshal(new_del()))
                await response.write_eof()
                return response
            
            data = data_map.get("dt")
            if data and len(data) > 0:
                socket_writer.write(data)
                await socket_writer.drain()
            
            return web.Response()
        
        # 创建新隧道
        if action != 0x00:
            return web.Response(status=400)
        
        response.headers['X-Accel-Buffering'] = 'no'
        
        host = data_map.get("h").decode()
        port = int(data_map.get("p").decode())
        
        if port == 0:
            port = request.transport.get_extra_info('sockname')[1]
        
        await response.prepare(request)
        
        if need_redirect:
            # 重定向流处理
            async with aiohttp.ClientSession() as session:
                async with session.request(
                    method=request.method,
                    url=redirect_url,
                    headers=self.prepare_headers(request, redirect_url),
                    data=marshal(data_map),
                    ssl=self.ssl_context
                ) as resp:
                    async for chunk in resp.content.iter_any():
                        await response.write(chunk)
        else:
            # 直接socket连接处理
            try:
                reader_socket, writer_socket = await asyncio.open_connection(host, port)
                ctx[client_id] = writer_socket
                
                await response.write(marshal(new_status(0x00)))
                
                while True:
                    chunk = await reader_socket.read(8192)
                    if not chunk:
                        break
                    await response.write(marshal(new_data(chunk)))
            except Exception:
                ctx.pop(client_id, None)
                await response.write(marshal(new_status(0x01)))
            finally:
                if client_id in ctx:
                    ctx.pop(client_id)
                    writer_socket.close()
                    await writer_socket.wait_closed()
        
        await response.write_eof()
        return response
    
    async def pipe_socket_to_http(self, reader, response):
        try:
            while True:
                data = await reader.read(8192)
                if not data:
                    break
                await response.write(marshal(new_data(data)))
        except Exception:
            pass
    
    async def pipe_http_to_socket(self, reader, writer):
        try:
            while True:
                data_map = await unmarshal(reader)
                action = data_map.get("ac")[0]
                
                if action == 0x02:
                    break
                elif action == 0x01:
                    data = data_map.get("dt")
                    if data and len(data) > 0:
                        writer.write(data)
                        await writer.drain()
                elif action == 0x03:
                    continue
                else:
                    break
        except Exception:
            pass
    
    async def redirect(self, request, data_map, redirect_url):
        headers = self.prepare_headers(request, redirect_url)
        body = marshal(data_map)
        
        async with aiohttp.ClientSession() as session:
            await session.request(
                method=request.method,
                url=redirect_url,
                headers=headers,
                data=body,
                ssl=self.ssl_context
            )
    
    def prepare_headers(self, request, redirect_url):
        parsed_url = urllib.parse.urlparse(redirect_url)
        headers = dict(request.headers)
        
        # 调整必要的头部
        headers['Content-Length'] = str(len(marshal({})))  # 将在发送时更新
        headers['Host'] = parsed_url.netloc
        headers['Connection'] = 'close'
        
        # 移除不需要的头部
        for key in ['Content-Encoding', 'Transfer-Encoding']:
            if key in headers:
                del headers[key]
                
        return headers

# 主应用入口点
async def init_app():
    suo5_handler = Suo5Handler()
    app = web.Application()
    app.router.add_route('*', '/{tail:.*}', suo5_handler.handle_request)
    return app

if __name__ == '__main__':
    app = asyncio.run(init_app())
    web.run_app(app, host='0.0.0.0', port=8080)