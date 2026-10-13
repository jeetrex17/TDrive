//go:build (darwin || ios) && cgo

#import <Foundation/Foundation.h>
#import <LocalAuthentication/LocalAuthentication.h>
#import <Security/Security.h>
#import <TargetConditionals.h>
#if TARGET_OS_OSX
#import <Security/SecTask.h>
#endif
#include <string.h>
#include "provider_apple.h"

enum {
    TDBioOK = 0, TDBioUnavailable = 1, TDBioCanceled = 2,
    TDBioLocked = 3, TDBioInvalidated = 4, TDBioMissing = 5,
    TDBioSigning = 6, TDBioFailed = 7,
};

static void tdrive_bio_zero(void *buffer, size_t size) {
    volatile unsigned char *bytes = buffer;
    while (size--) *bytes++ = 0;
}

@interface TDriveBiometricContext : NSObject
@property(nonatomic, strong) LAContext *authentication;
@property(atomic) BOOL canceled;
@end

@implementation TDriveBiometricContext
@end

static int tdrive_bio_la_error(NSError *error) {
    if (![error.domain isEqualToString:LAErrorDomain]) return TDBioFailed;
    switch (error.code) {
        case LAErrorUserCancel:
        case LAErrorUserFallback:
        case LAErrorSystemCancel:
        case LAErrorAppCancel:
        case LAErrorInvalidContext: return TDBioCanceled;
        case LAErrorBiometryLockout: return TDBioLocked;
        case LAErrorBiometryNotAvailable:
        case LAErrorBiometryNotEnrolled:
        case LAErrorPasscodeNotSet:
        case LAErrorNotInteractive: return TDBioUnavailable;
        default: return TDBioFailed;
    }
}

static int tdrive_bio_sec_error(OSStatus status) {
    switch (status) {
        case errSecSuccess: return TDBioOK;
        case errSecUserCanceled: return TDBioCanceled;
        case errSecItemNotFound: return TDBioMissing;
        case errSecAuthFailed: return TDBioInvalidated;
        case errSecMissingEntitlement: return TDBioSigning;
        case errSecNotAvailable:
        case errSecInteractionNotAllowed: return TDBioUnavailable;
        default: return TDBioFailed;
    }
}

static BOOL tdrive_bio_has_entitlement(void) {
#if TARGET_OS_OSX
    SecTaskRef task = SecTaskCreateFromSelf(kCFAllocatorDefault);
    if (!task) return NO;
    CFTypeRef groups = SecTaskCopyValueForEntitlement(task, CFSTR("keychain-access-groups"), NULL);
    CFTypeRef app = SecTaskCopyValueForEntitlement(task, CFSTR("com.apple.application-identifier"), NULL);
    BOOL present = (groups && CFGetTypeID(groups) == CFArrayGetTypeID() && CFArrayGetCount(groups) > 0)
        || (app && CFGetTypeID(app) == CFStringGetTypeID() && CFStringGetLength(app) > 0);
    if (groups) CFRelease(groups);
    if (app) CFRelease(app);
    CFRelease(task);
    return present;
#else
    // iOS enforces app identity and Keychain entitlements when installing.
    // Face ID requires this key even before the first prompt is requested.
    NSString *description = [[NSBundle mainBundle] objectForInfoDictionaryKey:@"NSFaceIDUsageDescription"];
    return [description isKindOfClass:[NSString class]] && description.length > 0;
#endif
}

static NSMutableDictionary *tdrive_bio_query(const char *account) {
    // Never allow the SecItem shim to select the legacy macOS file Keychain.
    return [@{
        (__bridge id)kSecClass: (__bridge id)kSecClassGenericPassword,
        (__bridge id)kSecAttrService: @"com.jeetraj.tdrive.biometric-vault.v1",
        (__bridge id)kSecAttrAccount: [NSString stringWithUTF8String:account],
        (__bridge id)kSecUseDataProtectionKeychain: @YES,
        (__bridge id)kSecAttrSynchronizable: @NO,
    } mutableCopy];
}

static int tdrive_bio_available(LAContext *context, int *kind) {
    if (!tdrive_bio_has_entitlement()) return TDBioSigning;
    NSError *error = nil;
    BOOL available = [context canEvaluatePolicy:LAPolicyDeviceOwnerAuthenticationWithBiometrics error:&error];
    if (kind) {
        *kind = context.biometryType == LABiometryTypeTouchID ? 1
            : context.biometryType == LABiometryTypeFaceID ? 2 : 0;
    }
    return available ? TDBioOK : tdrive_bio_la_error(error);
}

void *tdrive_bio_context_create(void) {
    @autoreleasepool {
        TDriveBiometricContext *context = [TDriveBiometricContext new];
        context.authentication = [LAContext new];
        context.authentication.localizedFallbackTitle = @"";
        context.authentication.touchIDAuthenticationAllowableReuseDuration = 0;
        context.authentication.localizedReason = @"unlock your encrypted files for this session";
        return (void *)CFBridgingRetain(context);
    }
}

void tdrive_bio_context_cancel(void *handle) {
    @autoreleasepool {
        TDriveBiometricContext *context = (__bridge TDriveBiometricContext *)handle;
        context.canceled = YES;
        [context.authentication invalidate];
    }
}

void tdrive_bio_context_release(void *handle) {
    if (handle) CFRelease(handle);
}

int tdrive_bio_status(const char *account, int *enrolled, int *kind) {
    @autoreleasepool {
        *enrolled = 0;
        *kind = 0;
        if (!tdrive_bio_has_entitlement()) return TDBioSigning;
        NSMutableDictionary *query = tdrive_bio_query(account);
        query[(__bridge id)kSecReturnAttributes] = @YES;
        LAContext *inspection = [LAContext new];
        inspection.interactionNotAllowed = YES;
        query[(__bridge id)kSecUseAuthenticationContext] = inspection;
        CFTypeRef attributes = NULL;
        OSStatus status = SecItemCopyMatching((__bridge CFDictionaryRef)query, &attributes);
        if (attributes) CFRelease(attributes);
        if (status == errSecSuccess || status == errSecInteractionNotAllowed) *enrolled = 1;
        else if (status != errSecItemNotFound) return tdrive_bio_sec_error(status);
        return tdrive_bio_available([LAContext new], kind);
    }
}

int tdrive_bio_enroll(void *handle, const char *account, const unsigned char *secret, size_t size) {
    @autoreleasepool {
        if (size != 64) return TDBioInvalidated;
        TDriveBiometricContext *context = (__bridge TDriveBiometricContext *)handle;
        if (context.canceled) return TDBioCanceled;
        int available = tdrive_bio_available(context.authentication, NULL);
        if (available != TDBioOK) return available;
        CFErrorRef accessError = NULL;
        SecAccessControlRef access = SecAccessControlCreateWithFlags(kCFAllocatorDefault,
            kSecAttrAccessibleWhenUnlockedThisDeviceOnly, kSecAccessControlBiometryCurrentSet, &accessError);
        if (accessError) CFRelease(accessError);
        if (!access) return TDBioUnavailable;
        dispatch_semaphore_t completed = dispatch_semaphore_create(0);
        __block int result = TDBioFailed;
        [context.authentication evaluateAccessControl:access operation:LAAccessControlOperationUseItem
            localizedReason:@"enable biometric unlock for your encrypted files"
            reply:^(BOOL success, NSError *error) {
                result = success ? TDBioOK : tdrive_bio_la_error(error);
                dispatch_semaphore_signal(completed);
            }];
        dispatch_semaphore_wait(completed, DISPATCH_TIME_FOREVER);
        if (context.canceled) result = TDBioCanceled;
        if (result != TDBioOK) {
            CFRelease(access);
            return result;
        }
        NSMutableData *payload = [NSMutableData dataWithLength:size];
        memcpy(payload.mutableBytes, secret, size);
        NSMutableDictionary *query = tdrive_bio_query(account);
        query[(__bridge id)kSecAttrAccessControl] = (__bridge id)access;
        query[(__bridge id)kSecValueData] = payload;
        query[(__bridge id)kSecUseAuthenticationContext] = context.authentication;
        int removed = tdrive_bio_delete(account);
        OSStatus status = removed == TDBioOK ? SecItemAdd((__bridge CFDictionaryRef)query, NULL) : errSecNotAvailable;
        tdrive_bio_zero(payload.mutableBytes, payload.length);
        CFRelease(access);
        if (context.canceled) {
            if (status == errSecSuccess) tdrive_bio_delete(account);
            return TDBioCanceled;
        }
        return tdrive_bio_sec_error(status);
    }
}

int tdrive_bio_unlock(void *handle, const char *account, unsigned char **secret, size_t *size) {
    @autoreleasepool {
        *secret = NULL;
        *size = 0;
        TDriveBiometricContext *context = (__bridge TDriveBiometricContext *)handle;
        if (context.canceled) return TDBioCanceled;
        int available = tdrive_bio_available(context.authentication, NULL);
        if (available != TDBioOK) return available;
        NSMutableDictionary *query = tdrive_bio_query(account);
        query[(__bridge id)kSecReturnData] = @YES;
        query[(__bridge id)kSecMatchLimit] = (__bridge id)kSecMatchLimitOne;
        query[(__bridge id)kSecUseAuthenticationContext] = context.authentication;
        CFTypeRef value = NULL;
        OSStatus status = SecItemCopyMatching((__bridge CFDictionaryRef)query, &value);
        int result = context.canceled ? TDBioCanceled : tdrive_bio_sec_error(status);
        if (result == TDBioOK) {
            if (!value || CFGetTypeID(value) != CFDataGetTypeID() || CFDataGetLength(value) != 64) {
                result = TDBioInvalidated;
            } else {
                *secret = malloc(64);
                if (!*secret) result = TDBioFailed;
                else {
                    memcpy(*secret, CFDataGetBytePtr(value), 64);
                    *size = 64;
                }
            }
        }
        if (value) CFRelease(value);
        return result;
    }
}

int tdrive_bio_delete(const char *account) {
    @autoreleasepool {
        if (!tdrive_bio_has_entitlement()) return TDBioSigning;
        NSMutableDictionary *query = tdrive_bio_query(account);
        OSStatus status = SecItemDelete((__bridge CFDictionaryRef)query);
        return status == errSecItemNotFound ? TDBioOK : tdrive_bio_sec_error(status);
    }
}

void tdrive_bio_secret_free(unsigned char *secret, size_t size) {
    if (secret) {
        tdrive_bio_zero(secret, size);
        free(secret);
    }
}
