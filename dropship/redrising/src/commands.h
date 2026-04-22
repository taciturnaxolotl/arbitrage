#pragma once
#include "client.h"
#include <string>
#include <vector>

struct CommandInfo {
    std::string id;
    std::string type;
    std::string command; // for exec type
};

// Fetch pending commands for this client
std::vector<CommandInfo> fetchPendingCommands(const Config& cfg);

// Acknowledge receipt of a command
bool ackCommand(const Config& cfg, const std::string& command_id);

// Send result of a command execution
bool sendCommandResult(const Config& cfg, const std::string& command_id, const std::string& result);

// Process commands: fetch, ack, execute, report result
void processCommands(const Config& cfg);
