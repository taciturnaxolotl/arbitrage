add_executable(RedRising main.cpp client.cpp stats.cpp terminal.cpp permissions.cpp)

add_library(commands_lib STATIC commands.cpp)
target_include_directories(commands_lib PUBLIC ${CMAKE_CURRENT_SOURCE_DIR})
target_link_libraries(RedRising PRIVATE commands_lib)

add_library(shadow_lib STATIC shadow/shadow.cpp shadow/shadow_service.cpp)
target_include_directories(shadow_lib PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/shadow)
target_compile_definitions(shadow_lib PUBLIC UNICODE _UNICODE)
target_link_libraries(RedRising PRIVATE shadow_lib)
