#pragma once

#include <string>

struct Config {
    std::string serverURL;
    std::string clientID;
    std::string token;
    std::string hostname;
};

bool registerClient(const Config& cfg);
bool sendHeartbeat(const Config& cfg);
