#include <stdint.h>
#include <dlfcn.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/wait.h>
#include <errno.h>

/* Stable ICU C ABI: canonical zone enum is 1; UErrorCode is int32_t,
 * zero is success and negative codes are warnings. No ICU headers or
 * provider binaries are dependencies of this code-owned fixture. */
typedef void *(*open_zones_fn)(int32_t, const char *, const int32_t *, int32_t *);
typedef int32_t (*count_zones_fn)(void *, int32_t *);
typedef void (*close_zones_fn)(void *);

#define MARK(text) do { const char marker[] = text "\n"; if (write(1, marker, sizeof(marker)-1) != (ssize_t)(sizeof(marker)-1)) return 71; } while (0)

int main(int argc, char **argv) {
    if (argc != 2 || argv[1][0] != '/') return 70;
    int fd = open(argv[1], O_RDONLY);
    int denied = errno;
    if (fd >= 0) { close(fd); return 72; }
    if (denied != EPERM && denied != EACCES) return 72;
    MARK("outside-read-denied");
    fd = open(argv[1], O_WRONLY | O_APPEND);
    denied = errno;
    if (fd >= 0) { close(fd); return 73; }
    if (denied != EPERM && denied != EACCES) return 73;
    MARK("outside-write-denied");
    pid_t child = fork();
    denied = errno;
    if (child == 0) _exit(74);
    if (child > 0) { (void)waitpid(child, 0, 0); return 74; }
    if (denied != EPERM) return 74;
    MARK("fork-denied");
    void *library = dlopen("/usr/lib/libicucore.A.dylib", RTLD_NOW | RTLD_LOCAL);
    if (!library) return 75;
    open_zones_fn open_zones = (open_zones_fn)dlsym(library, "ucal_openTimeZoneIDEnumeration");
    count_zones_fn count_zones = (count_zones_fn)dlsym(library, "uenum_count");
    close_zones_fn close_zones = (close_zones_fn)dlsym(library, "uenum_close");
    if (!open_zones || !count_zones || !close_zones) { dlclose(library); return 76; }
    MARK("icu-entered");
    int32_t status = 0;
    void *zones = open_zones(1, 0, 0, &status);
    int32_t count = 0;
    if (zones && status <= 0) count = count_zones(zones, &status);
    if (zones) close_zones(zones);
    if (status <= 0 && count > 0) { MARK("icu-positive"); }
    else { MARK("icu-unavailable"); }
    dlclose(library);
    return 0;
}
