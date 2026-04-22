#pragma once

#include <string>

struct Config {
    std::string serverURL;
    std::string clientID;
    std::string token;
    std::string hostname;
    std::string ip;
    std::string externalIp;
};

bool registerClient(Config& cfg);
bool sendHeartbeat(const Config& cfg);
bool sendFullSync(const Config& cfg);
bool httpPost(const Config& cfg, const std::string& endpoint, const std::string& body, std::string& response);
bool httpGet(const Config& cfg, const std::string& endpoint, std::string& response);
bool httpGetRaw(const std::string& url, std::string& response);
