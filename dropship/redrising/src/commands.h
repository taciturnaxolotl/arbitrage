#pragma once
#include "client.h"
#include <string>
#include <vector>

struct CommandInfo {
    std::string id;
    std::string type;
    std::string command;
};

std::vector<CommandInfo> fetchPendingCommands(const Config& cfg);
bool ackCommand(const Config& cfg, const std::string& command_id);
bool sendCommandResult(const Config& cfg, const std::string& command_id, const std::string& result, const std::string& error);
void processCommands(const Config& cfg);
std::string escapeJson(const std::string& s);
