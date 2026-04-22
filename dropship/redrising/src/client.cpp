#include "client.h"
#include <windows.h>
#include <winhttp.h>
#include <iostream>
#include <sstream>
#include <vector>

#pragma comment(lib, "winhttp.lib")

static bool httpPost(const std::string& server, const std::string& endpoint, const std::string& body, std::string& response) {
    // Simple WinHTTP POST implementation
    // Parse server URL
    URL_COMPONENTS urlComp{};
    urlComp.dwStructSize = sizeof(urlComp);
    urlComp.dwSchemeLength = (DWORD)-1;
    urlComp.dwHostNameLength = (DWORD)-1;
    urlComp.dwUrlPathLength = (DWORD)-1;
    std::wstring wurl(server.begin(), server.end());
    if (!WinHttpCrackUrl(wurl.c_str(), (DWORD)wurl.length(), 0, &urlComp)) {
        std::cerr << "Failed to parse URL" << std::endl;
        return false;
    }
    std::wstring host(urlComp.lpszHostName, urlComp.dwHostNameLength);
    std::wstring path(urlComp.lpszUrlPath, urlComp.dwUrlPathLength);
    std::wstring fullPath = path + L"" + std::wstring(endpoint.begin(), endpoint.end());
    HINTERNET hSession = WinHttpOpen(L"DarwiniumClient/1.0", WINHTTP_ACCESS_TYPE_DEFAULT_PROXY, WINHTTP_NO_PROXY_NAME, WINHTTP_NO_PROXY_BYPASS, 0);
    if (!hSession) return false;
    HINTERNET hConnect = WinHttpConnect(hSession, host.c_str(), urlComp.nPort, 0);
    if (!hConnect) { WinHttpCloseHandle(hSession); return false; }
    HINTERNET hRequest = WinHttpOpenRequest(hConnect, L"POST", fullPath.c_str(), NULL, WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES, (urlComp.nScheme == INTERNET_SCHEME_HTTPS) ? WINHTTP_FLAG_SECURE : 0);
    if (!hRequest) { WinHttpCloseHandle(hConnect); WinHttpCloseHandle(hSession); return false; }
    std::wstring wbody(body.begin(), body.end());
    BOOL bResults = WinHttpSendRequest(hRequest, WINHTTP_NO_ADDITIONAL_HEADERS, 0, (LPVOID)wbody.c_str(), (DWORD)(wbody.size()*2), (DWORD)(wbody.size()*2), 0);
    if (!bResults) { WinHttpCloseHandle(hRequest); WinHttpCloseHandle(hConnect); WinHttpCloseHandle(hSession); return false; }
    bResults = WinHttpReceiveResponse(hRequest, NULL);
    if (!bResults) { WinHttpCloseHandle(hRequest); WinHttpCloseHandle(hConnect); WinHttpCloseHandle(hSession); return false; }
    DWORD dwSize = 0;
    std::string resp;
    while (WinHttpQueryDataAvailable(hRequest, &dwSize) && dwSize) {
        std::vector<char> buffer(dwSize);
        DWORD dwDownloaded = 0;
        if (WinHttpReadData(hRequest, buffer.data(), dwSize, &dwDownloaded)) {
            resp.append(buffer.data(), dwDownloaded);
        }
    }
    response = resp;
    WinHttpCloseHandle(hRequest);
    WinHttpCloseHandle(hConnect);
    WinHttpCloseHandle(hSession);
    return true;
}

bool registerClient(const Config& cfg) {
    // Build JSON payload (simplified)
    std::ostringstream oss;
    oss << "{\"hostname\":\"" << cfg.hostname << "\",\"os\":\"Windows\",\"arch\":\"x86_64\"}";
    std::string resp;
    std::string endpoint = "/api/clients"; // POST to register (example)
    return httpPost(cfg.serverURL, endpoint, oss.str(), resp);
}

bool sendHeartbeat(const Config& cfg) {
    // Simplified heartbeat payload
    std::ostringstream oss;
    oss << "{\"data_hash\":\"dummy\"}";
    std::string resp;
    std::string endpoint = "/api/heartbeat";
    return httpPost(cfg.serverURL, endpoint, oss.str(), resp);
}
