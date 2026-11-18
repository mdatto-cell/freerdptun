#!/usr/bin/env python3
"""Генерирует ios/FreeRDPTunIOS.xcodeproj (без XcodeGen). Запуск: python3 ios/scripts/gen_xcodeproj.py"""
import hashlib, os
ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
def uid(s): return hashlib.sha1(s.encode()).hexdigest()[:24].upper()

files = {  # path -> (type, name)
 "App/FreeRDPTunApp.swift":"sourcecode.swift","App/RootView.swift":"sourcecode.swift",
 "App/AddProfileView.swift":"sourcecode.swift","App/ProfileEditView.swift":"sourcecode.swift",
 "App/LogView.swift":"sourcecode.swift","App/Profile.swift":"sourcecode.swift",
 "App/TunnelController.swift":"sourcecode.swift","Shared/TunnelConfig.swift":"sourcecode.swift",
 "Tunnel/PacketTunnelProvider.swift":"sourcecode.swift","Tunnel/frt_glue.c":"sourcecode.c.c",
 "Shared/FRTCore.h":"sourcecode.c.h","Tunnel/Tunnel-Bridging-Header.h":"sourcecode.c.h",
 "App/Info.plist":"text.plist.xml","Tunnel/Info.plist":"text.plist.xml",
 "App/App.entitlements":"text.plist.entitlements","Tunnel/Tunnel.entitlements":"text.plist.entitlements",
 "Libs/frt.xcconfig":"text.xcconfig",
}
app_src = ["App/FreeRDPTunApp.swift","App/RootView.swift","App/AddProfileView.swift","App/ProfileEditView.swift",
           "App/LogView.swift","App/Profile.swift","App/TunnelController.swift","Shared/TunnelConfig.swift"]
ext_src = ["Tunnel/PacketTunnelProvider.swift","Tunnel/frt_glue.c","Shared/TunnelConfig.swift"]
fr = {p: uid("fr:"+p) for p in files}
bf = {}
def buildfile(target, p): 
    k=(target,p); bf[k]=uid(f"bf:{target}:{p}"); return bf[k]
app_bf=[buildfile("app",p) for p in app_src]; ext_bf=[buildfile("ext",p) for p in ext_src]
APP_PROD, EXT_PROD = uid("prod:app"), uid("prod:ext")
EMBED_BF = uid("bf:embed"); EMBED_PH = uid("ph:embed")
APP_SRC_PH, EXT_SRC_PH = uid("ph:app:src"), uid("ph:ext:src")
APP_FW_PH, EXT_FW_PH = uid("ph:app:fw"), uid("ph:ext:fw")
APP_RES_PH, EXT_RES_PH = uid("ph:app:res"), uid("ph:ext:res")
APP_T, EXT_T = uid("t:app"), uid("t:ext")
PROJ, MAINGRP, PRODGRP = uid("proj"), uid("g:main"), uid("g:prod")
DEP, PROXY = uid("dep"), uid("proxy")
CL_PROJ, CL_APP, CL_EXT = uid("cl:proj"), uid("cl:app"), uid("cl:ext")
def cfg(name, t): return uid(f"cfg:{t}:{name}")
groups = {"App":[p for p in files if p.startswith("App/")],
          "Shared":[p for p in files if p.startswith("Shared/")],
          "Tunnel":[p for p in files if p.startswith("Tunnel/")],
          "Libs":["Libs/frt.xcconfig"]}
gid = {g: uid("g:"+g) for g in groups}

o=[]
w=o.append
w("// !$*UTF8*$!\n{\n\tarchiveVersion = 1;\n\tclasses = {\n\t};\n\tobjectVersion = 56;\n\tobjects = {\n")
w("/* Begin PBXBuildFile section */")
for (t,p),i in bf.items():
    w(f"\t\t{i} = {{isa = PBXBuildFile; fileRef = {fr[p]}; }};")
w(f"\t\t{EMBED_BF} = {{isa = PBXBuildFile; fileRef = {EXT_PROD}; settings = {{ATTRIBUTES = (RemoveHeadersOnCopy, ); }}; }};")
w("/* End PBXBuildFile section */\n")
w("/* Begin PBXContainerItemProxy section */")
w(f"\t\t{PROXY} = {{isa = PBXContainerItemProxy; containerPortal = {PROJ}; proxyType = 1; remoteGlobalIDString = {EXT_T}; remoteInfo = PacketTunnel; }};")
w("/* End PBXContainerItemProxy section */\n")
w("/* Begin PBXCopyFilesBuildPhase section */")
w(f"\t\t{EMBED_PH} = {{isa = PBXCopyFilesBuildPhase; buildActionMask = 2147483647; dstPath = \"\"; dstSubfolderSpec = 13; files = ({EMBED_BF}, ); name = \"Embed Foundation Extensions\"; runOnlyForDeploymentPostprocessing = 0; }};")
w("/* End PBXCopyFilesBuildPhase section */\n")
w("/* Begin PBXFileReference section */")
for p,t in files.items():
    w(f"\t\t{fr[p]} = {{isa = PBXFileReference; lastKnownFileType = {t}; path = {os.path.basename(p)}; sourceTree = \"<group>\"; }};")
w(f"\t\t{APP_PROD} = {{isa = PBXFileReference; explicitFileType = wrapper.application; includeInIndex = 0; path = FreeRDPTun.app; sourceTree = BUILT_PRODUCTS_DIR; }};")
w(f"\t\t{EXT_PROD} = {{isa = PBXFileReference; explicitFileType = \"wrapper.app-extension\"; includeInIndex = 0; path = PacketTunnel.appex; sourceTree = BUILT_PRODUCTS_DIR; }};")
w("/* End PBXFileReference section */\n")
w("/* Begin PBXFrameworksBuildPhase section */")
for i in (APP_FW_PH, EXT_FW_PH):
    w(f"\t\t{i} = {{isa = PBXFrameworksBuildPhase; buildActionMask = 2147483647; files = (); runOnlyForDeploymentPostprocessing = 0; }};")
w("/* End PBXFrameworksBuildPhase section */\n")
w("/* Begin PBXGroup section */")
w(f"\t\t{MAINGRP} = {{isa = PBXGroup; children = ({', '.join(gid[g] for g in groups)}, {PRODGRP}, ); sourceTree = \"<group>\"; }};")
for g,ps in groups.items():
    w(f"\t\t{gid[g]} = {{isa = PBXGroup; children = ({', '.join(fr[p] for p in ps)}, ); path = {g}; sourceTree = \"<group>\"; }};")
w(f"\t\t{PRODGRP} = {{isa = PBXGroup; children = ({APP_PROD}, {EXT_PROD}, ); name = Products; sourceTree = \"<group>\"; }};")
w("/* End PBXGroup section */\n")
w("/* Begin PBXNativeTarget section */")
w(f"\t\t{APP_T} = {{isa = PBXNativeTarget; buildConfigurationList = {CL_APP}; buildPhases = ({APP_SRC_PH}, {APP_FW_PH}, {APP_RES_PH}, {EMBED_PH}, ); buildRules = (); dependencies = ({DEP}, ); name = FreeRDPTun; productName = FreeRDPTun; productReference = {APP_PROD}; productType = \"com.apple.product-type.application\"; }};")
w(f"\t\t{EXT_T} = {{isa = PBXNativeTarget; baseConfigurationReference = ; buildConfigurationList = {CL_EXT}; buildPhases = ({EXT_SRC_PH}, {EXT_FW_PH}, {EXT_RES_PH}, ); buildRules = (); dependencies = (); name = PacketTunnel; productName = PacketTunnel; productReference = {EXT_PROD}; productType = \"com.apple.product-type.app-extension\"; }};".replace("baseConfigurationReference = ; ",""))
w("/* End PBXNativeTarget section */\n")
w("/* Begin PBXProject section */")
w(f"\t\t{PROJ} = {{isa = PBXProject; attributes = {{BuildIndependentTargetsInParallel = 1; LastSwiftUpdateCheck = 1500; LastUpgradeCheck = 1500; }}; buildConfigurationList = {CL_PROJ}; compatibilityVersion = \"Xcode 14.0\"; developmentRegion = ru; hasScannedForEncodings = 0; knownRegions = (ru, en, Base, ); mainGroup = {MAINGRP}; productRefGroup = {PRODGRP}; projectDirPath = \"\"; projectRoot = \"\"; targets = ({APP_T}, {EXT_T}, ); }};")
w("/* End PBXProject section */\n")
w("/* Begin PBXResourcesBuildPhase section */")
for i in (APP_RES_PH, EXT_RES_PH):
    w(f"\t\t{i} = {{isa = PBXResourcesBuildPhase; buildActionMask = 2147483647; files = (); runOnlyForDeploymentPostprocessing = 0; }};")
w("/* End PBXResourcesBuildPhase section */\n")
w("/* Begin PBXSourcesBuildPhase section */")
w(f"\t\t{APP_SRC_PH} = {{isa = PBXSourcesBuildPhase; buildActionMask = 2147483647; files = ({', '.join(app_bf)}, ); runOnlyForDeploymentPostprocessing = 0; }};")
w(f"\t\t{EXT_SRC_PH} = {{isa = PBXSourcesBuildPhase; buildActionMask = 2147483647; files = ({', '.join(ext_bf)}, ); runOnlyForDeploymentPostprocessing = 0; }};")
w("/* End PBXSourcesBuildPhase section */\n")
w("/* Begin PBXTargetDependency section */")
w(f"\t\t{DEP} = {{isa = PBXTargetDependency; target = {EXT_T}; targetProxy = {PROXY}; }};")
w("/* End PBXTargetDependency section */\n")

def settings(d):
    out=[]
    for k,v in d.items():
        if isinstance(v,list): v="(" + ", ".join(f'"{x}"' for x in v) + ", )"
        else: v=f'"{v}"'
        out.append(f"\t\t\t\t{k} = {v};")
    return "\n".join(out)
proj_common = {"ALWAYS_SEARCH_USER_PATHS":"NO","CLANG_ENABLE_MODULES":"YES","CLANG_ENABLE_OBJC_ARC":"YES",
  "ENABLE_USER_SCRIPT_SANDBOXING":"NO","IPHONEOS_DEPLOYMENT_TARGET":"16.0","SDKROOT":"iphoneos",
  "SWIFT_VERSION":"5.0","TARGETED_DEVICE_FAMILY":"1","MARKETING_VERSION":"0.1.3","CURRENT_PROJECT_VERSION":"1",
  "CODE_SIGN_STYLE":"Automatic","DEVELOPMENT_TEAM":"","ENABLE_BITCODE":"NO","GCC_C_LANGUAGE_STANDARD":"gnu11"}
dbg = dict(proj_common, DEBUG_INFORMATION_FORMAT="dwarf", ONLY_ACTIVE_ARCH="YES", SWIFT_OPTIMIZATION_LEVEL="-Onone",
           GCC_OPTIMIZATION_LEVEL="0", SWIFT_ACTIVE_COMPILATION_CONDITIONS="DEBUG")
rel = dict(proj_common, DEBUG_INFORMATION_FORMAT="dwarf-with-dsym", SWIFT_OPTIMIZATION_LEVEL="-O", VALIDATE_PRODUCT="YES")
app_s = {"PRODUCT_BUNDLE_IDENTIFIER":"com.example.freerdptun","PRODUCT_NAME":"FreeRDPTun","INFOPLIST_FILE":"App/Info.plist",
  "CODE_SIGN_ENTITLEMENTS":"App/App.entitlements","GENERATE_INFOPLIST_FILE":"NO",
  "LD_RUNPATH_SEARCH_PATHS":"$(inherited) @executable_path/Frameworks","ASSETCATALOG_COMPILER_APPICON_NAME":""}
ext_s = {"PRODUCT_BUNDLE_IDENTIFIER":"com.example.freerdptun.tunnel","PRODUCT_NAME":"PacketTunnel","INFOPLIST_FILE":"Tunnel/Info.plist",
  "CODE_SIGN_ENTITLEMENTS":"Tunnel/Tunnel.entitlements","GENERATE_INFOPLIST_FILE":"NO","SKIP_INSTALL":"YES",
  "SWIFT_OBJC_BRIDGING_HEADER":"Tunnel/Tunnel-Bridging-Header.h",
  "HEADER_SEARCH_PATHS":["$(SRCROOT)/Shared","$(SRCROOT)/../native"],
  "LIBRARY_SEARCH_PATHS":["$(SRCROOT)/Libs/lib"],
  "OTHER_LDFLAGS":"$(inherited) $(FRT_LIBS) -lresolv -lz -framework Security -framework CoreFoundation -framework SystemConfiguration",
  "LD_RUNPATH_SEARCH_PATHS":"$(inherited) @executable_path/Frameworks @executable_path/../../Frameworks"}
w("/* Begin XCBuildConfiguration section */")
cfgs={}
for t,(d,r) in {"proj":(dbg,rel),"app":(dict(app_s),dict(app_s)),"ext":(dict(ext_s),dict(ext_s))}.items():
    for name,s in (("Debug",d),("Release",r)):
        i=cfg(name,t); cfgs[(t,name)]=i
        base = f" baseConfigurationReference = {fr['Libs/frt.xcconfig']};" if t=="ext" else ""
        w(f"\t\t{i} = {{isa = XCBuildConfiguration;{base} buildSettings = {{\n{settings(s)}\n\t\t\t}}; name = {name}; }};")
w("/* End XCBuildConfiguration section */\n")
w("/* Begin XCConfigurationList section */")
for l,t in ((CL_PROJ,"proj"),(CL_APP,"app"),(CL_EXT,"ext")):
    w(f"\t\t{l} = {{isa = XCConfigurationList; buildConfigurations = ({cfgs[(t,'Debug')]}, {cfgs[(t,'Release')]}, ); defaultConfigurationIsVisible = 0; defaultConfigurationName = Release; }};")
w("/* End XCConfigurationList section */")
w(f"\t}};\n\trootObject = {PROJ};\n}}")
d=os.path.join(ROOT,"FreeRDPTunIOS.xcodeproj"); os.makedirs(d,exist_ok=True)
open(os.path.join(d,"project.pbxproj"),"w").write("\n".join(o)+"\n")
# scheme
sd=os.path.join(d,"xcshareddata","xcschemes"); os.makedirs(sd,exist_ok=True)
open(os.path.join(sd,"FreeRDPTun.xcscheme"),"w").write(f'''<?xml version="1.0" encoding="UTF-8"?>
<Scheme LastUpgradeVersion="1500" version="1.7"><BuildAction parallelizeBuildables="YES" buildImplicitDependencies="YES"><BuildActionEntries>
<BuildActionEntry buildForTesting="NO" buildForRunning="YES" buildForProfiling="YES" buildForArchiving="YES" buildForAnalyzing="YES"><BuildableReference BuildableIdentifier="primary" BlueprintIdentifier="{APP_T}" BuildableName="FreeRDPTun.app" BlueprintName="FreeRDPTun" ReferencedContainer="container:FreeRDPTunIOS.xcodeproj"/></BuildActionEntry>
</BuildActionEntries></BuildAction><LaunchAction buildConfiguration="Debug" selectedDebuggerIdentifier="Xcode.DebuggerFoundation.Debugger.LLDB" selectedLauncherIdentifier="Xcode.DebuggerFoundation.Launcher.LLDB" launchStyle="0" useCustomWorkingDirectory="NO" ignoresPersistentStateOnLaunch="NO" debugDocumentVersioning="YES" debugServiceExtension="internal" allowLocationSimulation="YES"><BuildableProductRunnable runnableDebuggingMode="0"><BuildableReference BuildableIdentifier="primary" BlueprintIdentifier="{APP_T}" BuildableName="FreeRDPTun.app" BlueprintName="FreeRDPTun" ReferencedContainer="container:FreeRDPTunIOS.xcodeproj"/></BuildableProductRunnable></LaunchAction>
<ArchiveAction buildConfiguration="Release" revealArchiveInOrganizer="YES"/></Scheme>
''')
print("ok")
