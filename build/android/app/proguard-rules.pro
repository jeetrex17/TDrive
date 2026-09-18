# Add project specific ProGuard rules here.
# You can control the set of applied configuration files using the
# proguardFiles setting in build.gradle.

# Keep native methods
-keepclasseswithmembernames class * {
    native <methods>;
}

# Keep Wails bridge classes
-keep class com.wails.app.WailsBridge { *; }
-keep class com.wails.app.WailsJSBridge { *; }

# Native Go workers resolve these decoders by class and method name.
-keep class com.wails.app.GalleryImage { *; }
-keep class com.wails.app.GalleryVideo { *; }
