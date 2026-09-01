import os, pty, select, time
def read_until(fd, stop_chars='› ', timeout=45):
    out = b''
    start = time.time()
    while True:
        if time.time() - start > timeout:
            break
        r, _, _ = select.select([fd], [], [], 0.5)
        if r:
            try:
                data = os.read(fd, 1024)
                if not data: break
                out += data
                if b'\xe2\x80\xba' in out: # unicode for ›
                    time.sleep(1.5)
                    while True:
                        r2, _, _ = select.select([fd], [], [], 0.2)
                        if r2:
                            d2 = os.read(fd, 1024)
                            if not d2: break
                            out += d2
                        else:
                            break
                    break
            except OSError:
                break
    return out.decode('utf-8', 'replace')

pid, fd = pty.fork()
if pid == 0:
    os.environ['TERM'] = 'xterm-256color'
    os.environ['NVIDIA_API_KEY'] = 'nvapi-REDACTED'
    os.environ['NABD_MODEL'] = 'nvidia/llama-3.1-nemotron-70b-instruct'
    os.execv('./ag', ['./ag'])
else:
    print("--- START ---")
    print(read_until(fd))
    
    print("\n--- TEST 1 ---")
    os.write(fd, "كم سطرًا في internal/agent/loop.go وما أطول دالة فيه؟\r".encode('utf-8'))
    print(read_until(fd))
    
    print("\n--- TEST 2 ---")
    os.write(fd, "أين تُستدعى Resolve؟\r".encode('utf-8'))
    print(read_until(fd))
    
    print("\n--- TEST 3 ---")
    os.write(fd, "اقرأ /etc/hosts\r".encode('utf-8'))
    print(read_until(fd))
    
    print("\n--- END ---")
    os.write(fd, b'\x03\x03')
