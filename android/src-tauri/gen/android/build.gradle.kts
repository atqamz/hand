buildscript {
    repositories {
        google()
        mavenCentral()
    }
    dependencies {
        classpath("com.android.tools.build:gradle:9.3.1")
        classpath("org.jetbrains.kotlin:kotlin-gradle-plugin:2.4.21")
        classpath("org.jetbrains.kotlin:kotlin-serialization:2.4.21")
        classpath("org.jetbrains.kotlin:compose-compiler-gradle-plugin:2.4.21")
        classpath("app.cash.paparazzi:paparazzi-gradle-plugin:2.0.0-alpha05.1")
    }
}

allprojects {
    repositories {
        google()
        mavenCentral()
    }
}

tasks.register("clean").configure {
    delete("build")
}

