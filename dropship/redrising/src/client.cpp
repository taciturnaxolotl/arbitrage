#include "client.h"
#include <windows.h>
#include <winhttp.h>
#include <iphlpapi.h>
#include <iostream>
#include <sstream>
#include <vector>
#include <regex>
#include "stats.h"

#pragma comment(lib, "winhttp.lib")
#pragma comment(lib, "iphlpapi.lib")

static std::string base64Encode(const std::string& input) {
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

static std::wstring s2w(const std::string& s) {
    return std::wstring(s.begin(), s.end());
}

bool httpPost(const Config& cfg, const std::string& endpoint, const std::string& body, std::string& response) {
    URL_COMPONENTS urlComp{};
    urlComp.dwStructSize = sizeof(urlComp);
    urlComp.dwSchemeLength = (DWORD)-1;
    urlComp.dwHostNameLength = (DWORD)-1;
    urlComp.dwUrlPathLength = (DWORD)-1;
    std::wstring wurl = s2w(cfg.serverURL);
    if (!WinHttpCrackUrl(wurl.c_str(), (DWORD)wurl.length(), 0, &urlComp)) return false;
    std::wstring host(urlComp.lpszHostName, urlComp.dwHostNameLength);
    std::wstring path(urlComp.lpszUrlPath, urlComp.dwUrlPathLength);
    std::wstring fullPath = path + s2w(endpoint);
    HINTERNET hSession = WinHttpOpen(L"DarwiniumClient/1.0", WINHTTP_ACCESS_TYPE_DEFAULT_PROXY, WINHTTP_NO_PROXY_NAME, WINHTTP_NO_PROXY_BYPASS, 0);
    if (!hSession) return false;
    HINTERNET hConnect = WinHttpConnect(hSession, host.c_str(), urlComp.nPort, 0);
    if (!hConnect) { WinHttpCloseHandle(hSession); return false; }
    HINTERNET hRequest = WinHttpOpenRequest(hConnect, L"POST", fullPath.c_str(), NULL, WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES, (urlComp.nScheme == INTERNET_SCHEME_HTTPS) ? WINHTTP_FLAG_SECURE : 0);
    if (!hRequest) { WinHttpCloseHandle(hConnect); WinHttpCloseHandle(hSession); return false; }
    if (!cfg.clientID.empty() && !cfg.token.empty()) {
        std::string authHeader = "Authorization: Basic " + base64Encode(cfg.clientID + ":" + cfg.token);
        std::wstring wauth = s2w(authHeader);
        WinHttpAddRequestHeaders(hRequest, wauth.c_str(), (ULONG)wauth.length(), WINHTTP_ADDREQ_FLAG_ADD);
    }
    std::wstring contentType = L"Content-Type: application/json";
    WinHttpAddRequestHeaders(hRequest, contentType.c_str(), (ULONG)contentType.length(), WINHTTP_ADDREQ_FLAG_ADD);
    std::wstring wbody = s2w(body);
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

bool httpGet(const Config& cfg, const std::string& endpoint, std::string& response) {
    URL_COMPONENTS urlComp{};
    urlComp.dwStructSize = sizeof(urlComp);
    urlComp.dwSchemeLength = (DWORD)-1;
    urlComp.dwHostNameLength = (DWORD)-1;
    urlComp.dwUrlPathLength = (DWORD)-1;
    std::wstring wurl = s2w(cfg.serverURL);
    if (!WinHttpCrackUrl(wurl.c_str(), (DWORD)wurl.length(), 0, &urlComp)) return false;
    std::wstring host(urlComp.lpszHostName, urlComp.dwHostNameLength);
    std::wstring path(urlComp.lpszUrlPath, urlComp.dwUrlPathLength);
    std::wstring fullPath = path + s2w(endpoint);
    HINTERNET hSession = WinHttpOpen(L"DarwiniumClient/1.0", WINHTTP_ACCESS_TYPE_DEFAULT_PROXY, WINHTTP_NO_PROXY_NAME, WINHTTP_NO_PROXY_BYPASS, 0);
    if (!hSession) return false;
    HINTERNET hConnect = WinHttpConnect(hSession, host.c_str(), urlComp.nPort, 0);
    if (!hConnect) { WinHttpCloseHandle(hSession); return false; }
    HINTERNET hRequest = WinHttpOpenRequest(hConnect, L"GET", fullPath.c_str(), NULL, WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES, (urlComp.nScheme == INTERNET_SCHEME_HTTPS) ? WINHTTP_FLAG_SECURE : 0);
    if (!hRequest) { WinHttpCloseHandle(hConnect); WinHttpCloseHandle(hSession); return false; }
    if (!cfg.clientID.empty() && !cfg.token.empty()) {
        std::string authHeader = "Authorization: Basic " + base64Encode(cfg.clientID + ":" + cfg.token);
        std::wstring wauth = s2w(authHeader);
        WinHttpAddRequestHeaders(hRequest, wauth.c_str(), (ULONG)wauth.length(), WINHTTP_ADDREQ_FLAG_ADD);
    }
    BOOL bResults = WinHttpSendRequest(hRequest, WINHTTP_NO_ADDITIONAL_HEADERS, 0, NULL, 0, 0, 0);
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

bool httpGetRaw(const std::string& url, std::string& response) {
    URL_COMPONENTS urlComp{};
    urlComp.dwStructSize = sizeof(urlComp);
    urlComp.dwSchemeLength = (DWORD)-1;
    urlComp.dwHostNameLength = (DWORD)-1;
    urlComp.dwUrlPathLength = (DWORD)-1;
    std::wstring wurl = s2w(url);
    if (!WinHttpCrackUrl(wurl.c_str(), (DWORD)wurl.length(), 0, &urlComp)) return false;
    std::wstring host(urlComp.lpszHostName, urlComp.dwHostNameLength);
    std::wstring path(urlComp.lpszUrlPath, urlComp.dwUrlPathLength);
    HINTERNET hSession = WinHttpOpen(L"RedRising/1.0", WINHTTP_ACCESS_TYPE_DEFAULT_PROXY, WINHTTP_NO_PROXY_NAME, WINHTTP_NO_PROXY_BYPASS, 0);
    if (!hSession) return false;
    HINTERNET hConnect = WinHttpConnect(hSession, host.c_str(), urlComp.nPort, 0);
    if (!hConnect) { WinHttpCloseHandle(hSession); return false; }
    HINTERNET hRequest = WinHttpOpenRequest(hConnect, L"GET", path.c_str(), NULL, WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES, (urlComp.nScheme == INTERNET_SCHEME_HTTPS) ? WINHTTP_FLAG_SECURE : 0);
    if (!hRequest) { WinHttpCloseHandle(hConnect); WinHttpCloseHandle(hSession); return false; }
    WinHttpSetTimeouts(hRequest, 5000, 5000, 5000, 5000);
    BOOL bResults = WinHttpSendRequest(hRequest, WINHTTP_NO_ADDITIONAL_HEADERS, 0, NULL, 0, 0, 0);
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

bool registerClient(Config& cfg) {
    char ipStr[64] = {0};
    PIP_ADAPTER_INFO adapterInfo = (PIP_ADAPTER_INFO)malloc(sizeof(IP_ADAPTER_INFO));
    ULONG outBufLen = sizeof(IP_ADAPTER_INFO);
    if (GetAdaptersInfo(adapterInfo, &outBufLen) == ERROR_BUFFER_OVERFLOW) {
        free(adapterInfo);
        adapterInfo = (PIP_ADAPTER_INFO)malloc(outBufLen);
    }
    if (adapterInfo && GetAdaptersInfo(adapterInfo, &outBufLen) == NO_ERROR) {
        strncpy_s(ipStr, adapterInfo->IpAddressList.IpAddress.String, _TRUNCATE);
    }
    free(adapterInfo);
    cfg.ip = ipStr;
    // Fetch external IP
    std::string extIp;
    std::string extResp;
    if (httpGetRaw("https://api.ipify.org", extResp) && !extResp.empty()) {
        extIp = extResp;
        // Trim whitespace
        while (!extIp.empty() && (extIp.back() == '\n' || extIp.back() == '\r' || extIp.back() == ' '))
            extIp.pop_back();
    }
    cfg.externalIp = extIp;
    std::ostringstream oss;
    oss << "{\"hostname\":\"" << cfg.hostname << "\","
        << "\"os\":\"Windows\","
        << "\"arch\":\"x86_64\","
        << "\"internal_ip\":\"" << escapeJson(ipStr) << "\","
        << "\"external_ip\":\"" << escapeJson(extIp) << "\","
        << "\"version\":\"1.0\"}";
    std::string resp;
    std::string endpoint = "/api/register";
    if (!httpPost(cfg, endpoint, oss.str(), resp)) return false;
    auto idPos = resp.find("\"id\":\"");
    auto tokenPos = resp.find("\"token\":\"");
    if (idPos != std::string::npos && tokenPos != std::string::npos) {
        size_t idStart = resp.find('"', idPos + 6) + 1;
        size_t idEnd = resp.find('"', idStart);
        size_t tokStart = resp.find('"', tokenPos + 9) + 1;
        size_t tokEnd = resp.find('"', tokStart);
        cfg.clientID = resp.substr(idStart, idEnd - idStart);
        cfg.token = resp.substr(tokStart, tokEnd - tokStart);
    }
    return !cfg.clientID.empty() && !cfg.token.empty();
}

static std::string escapeJson(const std::string& s) {
    std::ostringstream o; for (auto c: s) {
        switch (c) {
            case '\\': o << "\\\\"; break;
            case '"': o << "\\\""; break;
            case '\n': o << "\\n"; break;
            case '\r': o << "\\r"; break;
            case '\t': o << "\\t"; break;
            default:
                if (static_cast<unsigned char>(c) < 0x20) {
                    o << "\\u" << std::hex << std::setw(4) << std::setfill('0') << (int)c;
                } else o << c;
        }
    } return o.str();
}

bool sendHeartbeat(const Config& cfg) {
    SystemStats ss = collectSystemStats();
    OSInfo oi = collectOSInfo(cfg);
    std::ostringstream oss;
    oss << "{\"data_hash\":\"\","
        << "\"internal_ip\":\"" << escapeJson(cfg.ip) << "\","
        << "\"external_ip\":\"" << escapeJson(cfg.externalIp) << "\","
        << "\"system_stats\":{"
        << "\"cpu_percent\":" << ss.cpu_percent << ","
        << "\"memory_percent\":" << ss.memory_percent << ","
        << "\"memory_total\":" << ss.memory_total << ","
        << "\"memory_used\":" << ss.memory_used << ","
        << "\"disk_total\":" << ss.disk_total << ","
        << "\"disk_used\":" << ss.disk_used << ","
        << "\"disk_percent\":" << ss.disk_percent << ","
        << "\"uptime_seconds\":" << ss.uptime_seconds << ","
        << "\"load_avg_1\":" << ss.load_avg1 << ","
        << "\"load_avg_5\":" << ss.load_avg5 << ","
        << "\"load_avg_15\":" << ss.load_avg15 << "},"
        << "\"os_info\":{"
        << "\"name\":\"" << escapeJson(oi.name) << "\","
        << "\"version\":\"" << escapeJson(oi.version) << "\","
        << "\"kernel\":\"" << escapeJson(oi.kernel) << "\","
        << "\"platform\":\"" << escapeJson(oi.platform) << "\","
        << "\"hostname\":\"" << escapeJson(oi.hostname) << "\","
        << "\"machine_id\":\"" << escapeJson(oi.machine_id) << "\","
        << "\"serial_number\":\"" << escapeJson(oi.serial_number) << "\"}}";
    std::string resp;
    std::string endpoint = "/api/heartbeat";
    return httpPost(cfg, endpoint, oss.str(), resp);
}

bool sendFullSync(const Config& cfg) {
    SystemStats ss = collectSystemStats();
    OSInfo oi = collectOSInfo(cfg);
    auto apps = collectApplications();
    auto procs = collectProcesses();
    std::ostringstream oss;
    oss << "{\"data_hash\":\"\","
        << "\"system_stats\":{"
        << "\"cpu_percent\":" << ss.cpu_percent << ","
        << "\"memory_percent\":" << ss.memory_percent << ","
        << "\"memory_total\":" << ss.memory_total << ","
        << "\"memory_used\":" << ss.memory_used << ","
        << "\"disk_total\":" << ss.disk_total << ","
        << "\"disk_used\":" << ss.disk_used << ","
        << "\"disk_percent\":" << ss.disk_percent << ","
        << "\"uptime_seconds\":" << ss.uptime_seconds << ","
        << "\"load_avg_1\":" << ss.load_avg1 << ","
        << "\"load_avg_5\":" << ss.load_avg5 << ","
        << "\"load_avg_15\":" << ss.load_avg15 << "},"
        << "\"os_info\":{"
        << "\"name\":\"" << escapeJson(oi.name) << "\","
        << "\"version\":\"" << escapeJson(oi.version) << "\","
        << "\"kernel\":\"" << escapeJson(oi.kernel) << "\","
        << "\"platform\":\"" << escapeJson(oi.platform) << "\","
        << "\"hostname\":\"" << escapeJson(oi.hostname) << "\","
        << "\"machine_id\":\"" << escapeJson(oi.machine_id) << "\","
        << "\"serial_number\":\"" << escapeJson(oi.serial_number) << "\"},";
    oss << "\"applications\":[";
    for (size_t i=0;i<apps.size();++i) {
        const auto &a = apps[i];
        oss << "{\"name\":\"" << escapeJson(a.name) << "\","
            << "\"version\":\"" << escapeJson(a.version) << "\","
            << "\"install_date\":\"" << escapeJson(a.install_date) << "\","
            << "\"publisher\":\"" << escapeJson(a.publisher) << "\","
            << "\"path\":\"" << escapeJson(a.path) << "\","
            << "\"arch_kind\":\"" << escapeJson(a.arch_kind) << "\","
            << "\"last_modified\":\"" << escapeJson(a.last_modified) << "\","
            << "\"signed_by\":[";
        for (size_t j=0;j<a.signed_by.size();++j) {
            oss << "\"" << escapeJson(a.signed_by[j]) << "\"";
            if (j+1<a.signed_by.size()) oss << ",";
        }
        oss << "]}";
        if (i+1<apps.size()) oss << ",";
    }
    oss << "],\"processes\":[";
    for (size_t i=0;i<procs.size();++i) {
        const auto &p = procs[i];
        oss << "{\"pid\":" << p.pid << ","
            << "\"name\":\"" << escapeJson(p.name) << "\","
            << "\"status\":\"" << escapeJson(p.status) << "\","
            << "\"cpu_percent\":" << p.cpu << ","
            << "\"memory_percent\":" << p.memory << ","
            << "\"command\":\"" << escapeJson(p.command) << "\","
            << "\"exe\":\"" << escapeJson(p.exe) << "\","
            << "\"cwd\":\"" << escapeJson(p.cwd) << "\","
            << "\"username\":\"" << escapeJson(p.username) << "\","
            << "\"ppid\":" << p.ppid << ","
            << "\"create_time\":" << p.create_time << ","
            << "\"num_threads\":" << p.num_threads << ","
            << "\"num_fds\":" << p.num_fds << ","
            << "\"rss\":" << p.rss << ","
            << "\"vms\":" << p.vms << ","
            << "\"read_bytes\":" << p.read_bytes << ","
            << "\"write_bytes\":" << p.write_bytes << "}";
        if (i+1<procs.size()) oss << ",";
    }
    oss << "]}";
    std::string resp;
    return httpPost(cfg, "/api/sync", oss.str(), resp);
}
