add_executable(RedRising main.cpp client.cpp)
add_library(shadow_lib STATIC shadow/shadow.cpp)
target_include_directories(shadow_lib PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/shadow)
target_link_libraries(RedRising PRIVATE shadow_lib)
