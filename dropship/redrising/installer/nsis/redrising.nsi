; NSIS installer for RedRising
!include "MUI2.nsh"

Name "RedRising Client"
OutFile "RedRisingSetup.exe"
InstallDir "$PROGRAMFILES\RedRising"
RequestExecutionLevel admin

!define MUI_LICENSEPAGE_CHECKBOX
!define MUI_LICENSEPAGE_CHECKBOX_TEXT "I authorize this software to run with SYSTEM privileges"
!define MUI_LICENSEPAGE_CHECKBOX_EXPLANATION "If you check this box the installer will register the client as a System service. You may leave it unchecked to install without that extra step."

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "$%CD%\installer\nsis\License.txt" ; optional, can be omitted
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_LANGUAGE "English"

Section "Install RedRising"
    SetOutPath "$INSTDIR"
    File "..\\..\\build\\src\\Release\\RedRising.exe"

    ; If the user checked the box, register as a System service
    StrCmp $MUI_LICENSEPAGE_CHECKED "1" 0 +3
        ; Service registration (optional – uncomment if desired)
        ; nsExec::ExecToLog "sc create RedRising binPath=\"$INSTDIR\\RedRising.exe\" start=auto obj=LocalSystem"
        ; nsExec::ExecToLog "sc start RedRising"
SectionEnd

Section "Uninstall"
    Delete "$INSTDIR\\RedRising.exe"
    RMDir "$INSTDIR"
    ; nsExec::ExecToLog "sc stop RedRising"
    ; nsExec::ExecToLog "sc delete RedRising"
SectionEnd

Function .onInit
    ; No abort – installation proceeds regardless of checkbox state
FunctionEnd